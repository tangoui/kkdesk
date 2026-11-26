package files

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server serves the host's file system to one Client.
type Server struct {
	logf atomic.Pointer[func(format string, args ...any)]

	ep      *endpoint
	allowed func() bool

	mu        sync.Mutex
	uploads   map[uint64]*incoming // file being received, by transfer
	downloads map[uint64]context.CancelCauseFunc
	canceled  map[uint64]bool
	sendSem   chan struct{} // one download sends at a time

	uploadEpoch uint64 // incremented when access is revoked or the channel closes
}

// NewServer serves files over ch. allowed is consulted on every request and
// every chunk (and polled while transfers run); when it returns false all
// requests fail with ErrDenied and running transfers are aborted. A nil
// allowed always allows.
func NewServer(ch Channel, allowed func() bool) *Server {
	if allowed == nil {
		allowed = func() bool { return true }
	}
	s := &Server{allowed: allowed, uploads: map[uint64]*incoming{}, downloads: map[uint64]context.CancelCauseFunc{},
		canceled: map[uint64]bool{}, sendSem: make(chan struct{}, 1)}
	s.ep = newEndpoint(ch, s.onJSON, s.onData, s.onClose)
	s.ep.start()
	go s.watch()
	return s
}

// SetLogf sets where one line per finished transfer is logged (default
// log.Printf).
func (s *Server) SetLogf(f func(format string, args ...any)) { s.logf.Store(&f) }

func (s *Server) log(format string, args ...any) {
	if f := s.logf.Load(); f != nil && *f != nil {
		(*f)(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Done is closed when the channel closes.
func (s *Server) Done() <-chan struct{} { return s.ep.closed }

// CheckAllowed aborts running transfers at once if allowed now returns
// false (call it after turning file access off).
func (s *Server) CheckAllowed() {
	if !s.allowed() {
		s.abortAll(ErrDenied, true)
	}
}

// watch notices a permission change while a transfer is idle.
func (s *Server) watch() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.ep.closed:
			return
		case <-t.C:
			if s.active() > 0 {
				s.CheckAllowed()
			}
		}
	}
}

// active returns the number of running transfers.
func (s *Server) active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.uploads) + len(s.downloads)
}

// abortAll stops every transfer; notify tells the client why.
func (s *Server) abortAll(cause error, notify bool) {
	s.mu.Lock()
	s.uploadEpoch++
	ups := s.uploads
	s.uploads = map[uint64]*incoming{}
	for _, cancel := range s.downloads {
		cancel(cause) // the sender goroutine notifies the client
	}
	s.mu.Unlock()
	for id, in := range ups {
		in.abort()
		if notify {
			_ = s.ep.send(msg{Op: "abort", Transfer: id, Error: codeOf(cause)})
		}
	}
}

func (s *Server) onClose() { s.abortAll(ErrClosed, false) }

func (s *Server) reply(id uint64, r msg, err error) {
	if err != nil {
		r = errMsg(id, err)
	} else {
		ok := okMsg(id)
		r.ID, r.OK = ok.ID, ok.OK
	}
	_ = s.ep.send(r)
}

func (s *Server) onJSON(m *msg) {
	if m.isResponse() || m.ID == 0 {
		return
	}
	if m.Op == "hello" {
		s.reply(m.ID, msg{Version: Version}, nil)
		return
	}
	if !s.allowed() {
		s.reply(m.ID, msg{}, ErrDenied)
		s.abortAll(ErrDenied, true)
		return
	}
	switch m.Op {
	case "home":
		go s.reply(m.ID, msg{Home: home()}, nil)
	case "list":
		go func() { l, err := list(m.Path); s.reply(m.ID, msg{Listing: l}, err) }()
	case "stat":
		go func() { e, err := stat(m.Path); s.reply(m.ID, msg{Entry: e}, err) }()
	case "mkdir":
		go func() { name, err := mkdir(m); s.reply(m.ID, msg{Name: name}, err) }()
	case "download":
		go s.download(m)
	// Upload messages are handled in order with the data chunks.
	case "upload_begin":
		s.reply(m.ID, msg{}, s.uploadBegin(m))
	case "end":
		name, err := s.uploadEnd(m)
		s.reply(m.ID, msg{Name: name}, err)
	case "cancel":
		s.cancel(m.Transfer)
		s.reply(m.ID, msg{}, nil)
	default:
		s.reply(m.ID, msg{}, &RemoteError{Code: codeInvalid, Detail: "unknown op " + m.Op})
	}
}

func (s *Server) onData(id uint64, off int64, data []byte) {
	if !s.allowed() {
		s.abortAll(ErrDenied, true)
		return
	}
	s.mu.Lock()
	in := s.uploads[id]
	s.mu.Unlock()
	if in == nil {
		return
	}
	if err := in.write(off, data); err != nil && !in.reported {
		in.reported = true
		_ = s.ep.send(msg{Op: "file_error", Transfer: id, Rel: in.rel, Error: codeOf(err), Detail: describe(err)})
	}
}

func (s *Server) cancel(id uint64) {
	s.mu.Lock()
	s.canceled[id] = true
	in := s.uploads[id]
	delete(s.uploads, id)
	if c := s.downloads[id]; c != nil {
		c(ErrCanceled)
	}
	s.mu.Unlock()
	if in != nil {
		in.abort()
	}
}

func (s *Server) uploadBegin(m *msg) error {
	s.mu.Lock()
	epoch := s.uploadEpoch
	err := s.uploadStatusLocked(m.Transfer, epoch)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	dir, err := absPath(m.Dir)
	if err != nil {
		return err
	}
	parts, err := splitRel(m.Rel, goos)
	if err != nil {
		return err
	}
	root, err := openReceiveRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	parent := filepath.Dir(filepath.Join(parts...))
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return localError(err)
	}
	in, err := newIncoming(root, parts, m.Size, m.Mode, m.MTime)
	if err != nil {
		return localError(err)
	}
	in.rel = m.Rel
	return s.registerUpload(m.Transfer, epoch, in)
}

func (s *Server) uploadStatusLocked(id, epoch uint64) error {
	switch {
	case s.ep.isClosed():
		return ErrClosed
	case epoch != s.uploadEpoch || !s.allowed():
		return ErrDenied
	case s.canceled[id]:
		return ErrCanceled
	default:
		return nil
	}
}

func (s *Server) registerUpload(id, epoch uint64, in *incoming) error {
	s.mu.Lock()
	err := s.uploadStatusLocked(id, epoch)
	old := s.uploads[id]
	if err == nil {
		s.uploads[id] = in
	}
	s.mu.Unlock()
	if err != nil {
		in.abort()
		return err
	}
	if old != nil {
		old.abort()
	}
	return nil
}

func (s *Server) uploadEnd(m *msg) (string, error) {
	s.mu.Lock()
	id, epoch := m.Transfer, s.uploadEpoch
	in := s.uploads[id]
	err := s.uploadStatusLocked(id, epoch)
	if err != nil {
		delete(s.uploads, id)
	}
	s.mu.Unlock()
	if err != nil {
		if in != nil {
			in.abort()
		}
		return "", err
	}
	if in == nil {
		return "", &RemoteError{Code: codeInvalid, Detail: "no file in progress"}
	}
	defer func() {
		s.mu.Lock()
		if s.uploads[id] == in {
			delete(s.uploads, id)
		}
		s.mu.Unlock()
	}()
	if m.Error != "" {
		in.abort()
		return "", errorFor(m.Error, m.Detail)
	}
	name, err := in.finish(m.SHA256, func(place func() error) error {
		return s.commitUpload(id, epoch, in, place)
	})
	if err != nil {
		s.mu.Lock()
		if stop := s.uploadStatusLocked(id, epoch); stop != nil {
			err = stop
		}
		s.mu.Unlock()
	}
	el := time.Since(in.started)
	s.log("files: received %s: %d bytes in %v (%.1f MB/s), err=%v",
		filepath.Join(in.logDir, name), in.wrote, el.Round(time.Millisecond), float64(in.wrote)/max(el.Seconds(), 1e-9)/1e6, err)
	return name, err
}

// commitUpload orders each attempt to publish the file against abortAll.
func (s *Server) commitUpload(id, epoch uint64, in *incoming, place func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.uploadStatusLocked(id, epoch); err != nil {
		return err
	}
	if s.uploads[id] != in {
		return ErrCanceled
	}
	err := place()
	if err == nil {
		delete(s.uploads, id)
	}
	return err
}

// download walks the requested path and streams it to the client.
func (s *Server) download(m *msg) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s.mu.Lock()
	canceled := s.canceled[m.Transfer]
	if !canceled {
		s.downloads[m.Transfer] = cancel
	}
	s.mu.Unlock()
	if canceled {
		s.reply(m.ID, msg{}, ErrCanceled)
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.downloads, m.Transfer)
		s.mu.Unlock()
	}()

	p, err := walkRemote(m.Path)
	if err != nil {
		s.reply(m.ID, msg{}, err)
		return
	}
	s.reply(m.ID, msg{Transfer: m.Transfer, Name: p.name, Size: p.total, Files: len(p.files) + len(p.failed), Skipped: p.skipped}, nil)

	select {
	case s.sendSem <- struct{}{}:
		defer func() { <-s.sendSem }()
	case <-ctx.Done():
		s.endDownload(m.Transfer, ctx, nil, 0, 0)
		return
	case <-s.ep.closed:
		return
	}
	start := time.Now()
	var sent int64
	files := 0
	failed := p.failed
	check := func() error {
		if !s.allowed() {
			cancel(ErrDenied)
		}
		return context.Cause(ctx)
	}
	for _, d := range p.dirs {
		if check() != nil {
			break
		}
		_ = s.ep.send(msg{Op: "dir", Transfer: m.Transfer, Rel: d})
	}
	buf := make([]byte, headerLen+ChunkSize)
	for _, f := range p.files {
		if check() != nil {
			break
		}
		src, err := os.Open(f.abs)
		if err != nil {
			_ = s.ep.send(msg{Op: "skip", Transfer: m.Transfer, Rel: f.rel, Size: f.size, Error: codeOf(err), Detail: describe(err)})
			continue
		}
		_ = s.ep.send(msg{Op: "file", Transfer: m.Transfer, Rel: f.rel, Size: f.size, Mode: f.mode, MTime: f.mtime})
		sum, err := sendFile(ctx, s.ep, m.Transfer, src, f.size, buf, check, func(n int) { sent += int64(n) })
		src.Close()
		if err != nil {
			if ctx.Err() != nil || s.ep.isClosed() {
				break
			}
			_ = s.ep.send(msg{Op: "end", Transfer: m.Transfer, Rel: f.rel, Error: codeOf(err), Detail: describe(err)})
			continue
		}
		_ = s.ep.send(msg{Op: "end", Transfer: m.Transfer, Rel: f.rel, SHA256: sum})
		files++
	}
	s.endDownload(m.Transfer, ctx, failed, files, sent)
	el := time.Since(start)
	s.log("files: sent %s: %d files, %d bytes in %v (%.1f MB/s), err=%v",
		m.Path, files, sent, el.Round(time.Millisecond), float64(sent)/max(el.Seconds(), 1e-9)/1e6, context.Cause(ctx))
}

func (s *Server) endDownload(id uint64, ctx context.Context, failed []wireFailure, files int, sent int64) {
	switch err := context.Cause(ctx); {
	case err == nil:
		_ = s.ep.send(msg{Op: "done", Transfer: id, Files: files, Size: sent, Failed: failed})
	case errors.Is(err, ErrCanceled), errors.Is(err, ErrClosed):
		// the client already knows
	default:
		_ = s.ep.send(msg{Op: "abort", Transfer: id, Error: codeOf(err)})
	}
}

type remotePlan struct {
	name    string
	dirs    []string
	files   []planFile
	skipped []string
	failed  []wireFailure
	total   int64
}

// walkRemote collects what a download of path sends: folders first (parents
// before children), then files. The root is followed if it is a symbolic
// link; links below it are skipped.
func walkRemote(path string) (*remotePlan, error) {
	root, err := absPath(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, localError(err)
	}
	base := filepath.Base(root)
	if base == string(filepath.Separator) || base == "." || strings.HasSuffix(base, ":"+string(filepath.Separator)) {
		return nil, &RemoteError{Code: codeInvalid, Detail: "cannot download a whole volume"}
	}
	p := &remotePlan{name: base}
	if !fi.IsDir() {
		if !fi.Mode().IsRegular() {
			return nil, &RemoteError{Code: codeInvalid, Detail: root + " is not a regular file"}
		}
		p.files = []planFile{{rel: base, abs: root, size: fi.Size(), mode: uint32(fi.Mode().Perm()), mtime: fi.ModTime().UnixNano()}}
		p.total = fi.Size()
		return p, nil
	}
	lp, err := walkLocal(root)
	if err != nil {
		return nil, err
	}
	p.dirs = append([]string{base}, lp.dirs...)
	p.files, p.skipped, p.total = lp.files, lp.skipped, lp.total
	for _, f := range lp.failed {
		code, detail := codeIO, f.Err.Error()
		var re *RemoteError
		if errors.As(f.Err, &re) {
			code, detail = re.Code, re.Detail
		}
		p.failed = append(p.failed, wireFailure{Rel: f.Path, Error: code, Detail: detail})
	}
	return p, nil
}

func home() *Home {
	h := &Home{Volumes: volumes(), Sep: string(filepath.Separator), OS: runtime.GOOS}
	h.Dir, _ = os.UserHomeDir()
	return h
}

func entryFor(path string, fi fs.FileInfo) Entry {
	e := Entry{Name: fi.Name(), Path: path, IsDir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime(), Hidden: isHidden(fi)}
	if fi.Mode()&fs.ModeSymlink != 0 {
		e.Link = true
		if t, err := os.Stat(path); err == nil {
			e.IsDir = t.IsDir()
			if !t.IsDir() {
				e.Size = t.Size()
			}
		}
	}
	if e.IsDir {
		e.Size = 0
	}
	return e
}

func stat(path string) (*Entry, error) {
	p, err := absPath(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, localError(err)
	}
	e := entryFor(p, fi)
	return &e, nil
}

func list(path string) (*Listing, error) {
	dir, err := absPath(path)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, localError(err)
	}
	l := &Listing{Path: dir, Entries: make([]Entry, 0, min(len(des), MaxList))}
	if parent := filepath.Dir(dir); parent != dir {
		l.Parent = parent
	}
	for _, d := range des {
		fi, err := d.Info()
		if err != nil {
			continue // vanished
		}
		l.Entries = append(l.Entries, entryFor(filepath.Join(dir, d.Name()), fi))
	}
	slices.SortFunc(l.Entries, func(a, b Entry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(l.Entries) > MaxList {
		l.Entries, l.Truncated = l.Entries[:MaxList], true
	}
	return l, nil
}

// mkdir creates m.Path, or m.Rel under m.Dir (with parents, an existing
// folder is fine, or with Unique a fresh "name (n)"). It returns the
// created folder's name.
func mkdir(m *msg) (string, error) {
	if m.Path != "" {
		p, err := absPath(m.Path)
		if err != nil {
			return "", err
		}
		if err := os.Mkdir(p, 0o755); err != nil {
			return "", localError(err)
		}
		return filepath.Base(p), nil
	}
	dir, err := absPath(m.Dir)
	if err != nil {
		return "", err
	}
	parts, err := splitRel(m.Rel, goos)
	if err != nil {
		return "", err
	}
	root, err := openReceiveRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if m.Unique {
		if len(parts) != 1 {
			return "", &RemoteError{Code: codeInvalid, Detail: "unique mkdir takes one name"}
		}
		return mkdirUnique(root, parts[0])
	}
	if err := root.MkdirAll(filepath.Join(parts...), 0o755); err != nil {
		return "", localError(err)
	}
	return parts[len(parts)-1], nil
}
