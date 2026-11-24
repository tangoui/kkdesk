package files

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Client browses and transfers files on the host (controller side).
type Client struct {
	logf atomic.Pointer[func(format string, args ...any)]

	ep *endpoint

	mu        sync.Mutex
	nextID    uint64
	pending   map[uint64]*pendingCall
	transfers map[uint64]*Transfer

	helloMu sync.Mutex
	helloOK bool

	upSem chan struct{} // one upload sends at a time
}

type pendingCall struct {
	ch      chan *msg
	onReply func(*msg) // runs on the message goroutine before ch is signalled
}

// NewClient starts a client on ch. The protocol version is checked with the
// first request.
func NewClient(ch Channel) *Client {
	c := &Client{pending: map[uint64]*pendingCall{}, transfers: map[uint64]*Transfer{}, upSem: make(chan struct{}, 1)}
	c.ep = newEndpoint(ch, c.onJSON, c.onData, c.onClose)
	c.ep.start()
	return c
}

// SetLogf sets where one line per finished transfer is logged (default
// log.Printf).
func (c *Client) SetLogf(f func(format string, args ...any)) { c.logf.Store(&f) }

func (c *Client) log(format string, args ...any) {
	if f := c.logf.Load(); f != nil && *f != nil {
		(*f)(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Done is closed when the channel closes.
func (c *Client) Done() <-chan struct{} { return c.ep.closed }

func (c *Client) newID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

// call sends a request and waits for its response.
func (c *Client) call(ctx context.Context, m msg, onReply func(*msg)) (*msg, error) {
	if m.ID == 0 {
		m.ID = c.newID()
	}
	pc := &pendingCall{ch: make(chan *msg, 1), onReply: onReply}
	c.mu.Lock()
	c.pending[m.ID] = pc
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, m.ID)
		c.mu.Unlock()
	}()
	if err := c.ep.send(m); err != nil {
		return nil, err
	}
	select {
	case r := <-pc.ch:
		if !*r.OK {
			return r, errorFor(r.Error, r.Detail)
		}
		return r, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-c.ep.closed:
		return nil, ErrClosed
	}
}

// request checks the protocol version once, then calls.
func (c *Client) request(ctx context.Context, m msg, onReply func(*msg)) (*msg, error) {
	c.helloMu.Lock()
	if !c.helloOK {
		r, err := c.call(ctx, msg{Op: "hello", Version: Version}, nil)
		if err != nil {
			c.helloMu.Unlock()
			return nil, err
		}
		if r.Version != Version {
			c.helloMu.Unlock()
			return nil, &RemoteError{Code: codeVersion, Detail: "host speaks version " + itoa(r.Version)}
		}
		c.helloOK = true
	}
	c.helloMu.Unlock()
	return c.call(ctx, m, onReply)
}

func (c *Client) onJSON(m *msg) {
	if m.isResponse() {
		c.mu.Lock()
		pc := c.pending[m.ID]
		c.mu.Unlock()
		if pc != nil {
			if pc.onReply != nil && *m.OK {
				pc.onReply(m)
			}
			pc.ch <- m
		}
		return
	}
	if t := c.transfer(m.Transfer); t != nil {
		t.handle(m)
	}
}

func (c *Client) onData(id uint64, off int64, data []byte) {
	if t := c.transfer(id); t != nil && !t.upload {
		t.data(off, data)
	}
}

func (c *Client) onClose() {
	c.mu.Lock()
	ts := make([]*Transfer, 0, len(c.transfers))
	for _, t := range c.transfers {
		ts = append(ts, t)
	}
	c.mu.Unlock()
	for _, t := range ts {
		t.finish(ErrClosed)
	}
}

func (c *Client) transfer(id uint64) *Transfer {
	if id == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transfers[id]
}

func (c *Client) forget(id uint64) {
	c.mu.Lock()
	delete(c.transfers, id)
	c.mu.Unlock()
}

func (c *Client) sendCancel(id uint64) {
	_ = c.ep.send(msg{ID: c.newID(), Op: "cancel", Transfer: id})
}

// Home returns the host's home directory and volumes.
func (c *Client) Home(ctx context.Context) (Home, error) {
	r, err := c.request(ctx, msg{Op: "home"}, nil)
	if err != nil {
		return Home{}, err
	}
	if r.Home == nil {
		return Home{}, &RemoteError{Code: codeInvalid, Detail: "empty home response"}
	}
	return *r.Home, nil
}

// List returns the entries of a directory on the host: directories first,
// then files, each sorted by name, at most MaxList (Truncated is set if
// there were more).
func (c *Client) List(ctx context.Context, path string) (Listing, error) {
	r, err := c.request(ctx, msg{Op: "list", Path: path}, nil)
	if err != nil {
		return Listing{}, err
	}
	if r.Listing == nil {
		return Listing{}, &RemoteError{Code: codeInvalid, Detail: "empty listing"}
	}
	return *r.Listing, nil
}

// Stat describes one path on the host.
func (c *Client) Stat(ctx context.Context, path string) (Entry, error) {
	r, err := c.request(ctx, msg{Op: "stat", Path: path}, nil)
	if err != nil {
		return Entry{}, err
	}
	if r.Entry == nil {
		return Entry{}, &RemoteError{Code: codeInvalid, Detail: "empty stat response"}
	}
	return *r.Entry, nil
}

// Mkdir creates one directory on the host; ErrExists if it exists.
func (c *Client) Mkdir(ctx context.Context, path string) error {
	_, err := c.request(ctx, msg{Op: "mkdir", Path: path}, nil)
	return err
}

func (c *Client) newTransfer(ctx context.Context, upload bool, progress func(Progress)) *Transfer {
	tctx, cancel := context.WithCancelCause(context.Background())
	t := &Transfer{id: c.newID(), c: c, upload: upload, ctx: tctx, cancel: cancel, meter: newMeter(progress), done: make(chan struct{}), failed: map[string]bool{}}
	c.mu.Lock()
	c.transfers[t.id] = t
	c.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { t.finish(context.Cause(ctx)); c.sendCancel(t.id) })
	go func() { <-t.done; stop() }()
	return t
}

// Download copies a file or folder from the host into localDir, renaming
// on conflicts. ctx bounds the whole transfer: canceling it cancels the
// transfer. progress, if set, is called from the receiving goroutine and
// must not block.
func (c *Client) Download(ctx context.Context, remotePath, localDir string, progress func(Progress)) (*Transfer, error) {
	if fi, err := os.Stat(localDir); err != nil {
		return nil, localError(err)
	} else if !fi.IsDir() {
		return nil, &RemoteError{Code: codeInvalid, Detail: localDir + " is not a directory"}
	}
	t := c.newTransfer(ctx, false, progress)
	t.localDir = localDir
	_, err := c.request(ctx, msg{Op: "download", Path: remotePath, Transfer: t.id}, func(r *msg) {
		// On the message goroutine, before any content of this transfer.
		t.mu.Lock()
		t.res.Name = r.Name
		t.res.Skipped = r.Skipped
		t.mu.Unlock()
		t.meter.update(true, func(p *Progress) { p.Total, p.Files, p.Name = r.Size, r.Files, r.Name })
	})
	if err != nil {
		t.finish(err)
		return nil, err
	}
	return t, nil
}

// handle processes a server notification for a download or upload.
func (t *Transfer) handle(m *msg) {
	switch m.Op {
	case "abort":
		t.finish(errorFor(m.Error, m.Detail))
		return
	case "file_error": // upload: the host could not write the current file
		t.mu.Lock()
		t.failed[m.Rel] = true
		t.mu.Unlock()
		return
	}
	if t.upload {
		return
	}
	var progress func(*Progress)
	force := false
	t.mu.Lock()
	if t.fin {
		t.mu.Unlock()
		return
	}
	switch m.Op {
	case "dir":
		parts, err := t.localParts(m.Rel)
		if err == nil && len(parts) == 1 && t.root == "" {
			// The top-level folder comes first; pick a free name for it.
			name := uniqueName(t.localDir, parts[0])
			if err = os.Mkdir(filepath.Join(t.localDir, name), 0o755); err == nil {
				t.root, t.res.Name = name, name
			}
		} else if err == nil {
			err = os.MkdirAll(joinRel(t.localDir, parts), 0o755)
		}
		if err != nil {
			t.addFailure(m.Rel, localError(err))
		}
	case "file":
		if t.cur != nil {
			t.cur.abort()
			t.cur = nil
		}
		t.curRel = m.Rel
		parts, err := t.localParts(m.Rel)
		if err == nil {
			t.cur, err = newIncoming(joinRel(t.localDir, parts[:len(parts)-1]), parts[len(parts)-1], m.Size, m.Mode, m.MTime)
			err = localError(err)
		}
		if err != nil {
			t.addFailure(m.Rel, err)
		}
		progress, force = func(p *Progress) { p.Name = filepath.Base(m.Rel) }, true
	case "end":
		in := t.cur
		t.cur = nil
		if in == nil || m.Rel != "" && m.Rel != t.curRel {
			break // already failed
		}
		var name string
		var err error
		if m.Error != "" {
			in.abort()
			err = errorFor(m.Error, m.Detail)
		} else {
			name, err = in.finish(m.SHA256)
		}
		if err != nil {
			t.addFailure(t.curRel, err)
		} else {
			t.res.Files++
			t.res.Bytes += in.size
			if !strings.Contains(t.curRel, "/") {
				t.res.Name = name
			}
		}
		progress, force = func(p *Progress) { p.FilesDone++ }, true
	case "skip": // the host could not read a file
		t.addFailure(m.Rel, errorFor(m.Error, m.Detail))
		progress, force = func(p *Progress) { p.FilesDone++; p.Done += m.Size }, true
	case "done":
		t.mu.Unlock()
		for _, f := range m.Failed {
			t.mu.Lock()
			t.addFailure(f.Rel, errorFor(f.Error, f.Detail))
			t.mu.Unlock()
		}
		t.meter.update(true, func(*Progress) {})
		t.finish(nil)
		return
	}
	t.mu.Unlock()
	if progress != nil {
		t.meter.update(force, progress)
	}
}

// localParts maps a remote relative path onto local, sanitized components,
// replacing the top-level folder with its conflict-free name.
func (t *Transfer) localParts(rel string) ([]string, error) {
	parts, err := splitRel(rel, goos)
	if err != nil {
		return nil, err
	}
	if len(parts) > 1 {
		if t.root == "" {
			return nil, &RemoteError{Code: codeInvalid, Detail: "content before its folder: " + rel}
		}
		parts[0] = t.root
	}
	return parts, nil
}

func (t *Transfer) data(off int64, data []byte) {
	t.mu.Lock()
	if t.fin {
		t.mu.Unlock()
		return
	}
	if t.cur != nil {
		_ = t.cur.write(off, data)
	}
	t.mu.Unlock()
	t.meter.update(false, func(p *Progress) { p.Done += int64(len(data)) })
}

// planFile is one local file of an upload.
type planFile struct {
	rel   string // "/"-separated, starting with the top-level name
	abs   string
	size  int64
	mode  uint32
	mtime int64
}

type plan struct {
	isDir   bool
	dirs    []string // rel paths below the top-level folder, parents first
	files   []planFile
	skipped []string
	failed  []FileError
	total   int64
}

// walkLocal collects what an upload of root will send. Symbolic links below
// the root are skipped; the root itself is followed.
func walkLocal(root string) (*plan, error) {
	root = filepath.Clean(root)
	fi, err := os.Stat(root)
	if err != nil {
		return nil, localError(err)
	}
	base := filepath.Base(root)
	p := &plan{isDir: fi.IsDir()}
	if !fi.IsDir() {
		if !fi.Mode().IsRegular() {
			return nil, &RemoteError{Code: codeInvalid, Detail: root + " is not a regular file"}
		}
		p.files = []planFile{{rel: base, abs: root, size: fi.Size(), mode: uint32(fi.Mode().Perm()), mtime: fi.ModTime().UnixNano()}}
		p.total = fi.Size()
		return p, nil
	}
	walkRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		walkRoot = r
	}
	err = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		r, _ := filepath.Rel(walkRoot, path)
		rel := base
		if r != "." {
			rel += "/" + filepath.ToSlash(r)
		}
		if err != nil {
			p.failed = append(p.failed, FileError{Path: rel, Err: localError(err)})
			return nil
		}
		if path == walkRoot {
			return nil
		}
		switch {
		case d.IsDir():
			p.dirs = append(p.dirs, rel)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				p.failed = append(p.failed, FileError{Path: rel, Err: localError(err)})
				return nil
			}
			p.files = append(p.files, planFile{rel: rel, abs: path, size: info.Size(), mode: uint32(info.Mode().Perm()), mtime: info.ModTime().UnixNano()})
			p.total += info.Size()
		default:
			p.skipped = append(p.skipped, rel)
		}
		return nil
	})
	return p, err
}

// Upload copies a local file or folder into remoteDir on the host, renaming
// on conflicts. Uploads run one at a time. ctx bounds the whole transfer.
func (c *Client) Upload(ctx context.Context, localPath, remoteDir string, progress func(Progress)) (*Transfer, error) {
	p, err := walkLocal(localPath)
	if err != nil {
		return nil, err
	}
	t := c.newTransfer(ctx, true, progress)
	t.res.Skipped = p.skipped
	t.res.Failed = p.failed
	t.res.Name = filepath.Base(filepath.Clean(localPath))
	t.meter.update(true, func(pr *Progress) {
		pr.Total, pr.Files, pr.Name = p.total, len(p.files)+len(p.failed), t.res.Name
	})
	go c.runUpload(t, p, remoteDir)
	return t, nil
}

// fatal reports errors that stop a whole transfer rather than one file.
func fatal(err error) bool {
	return errors.Is(err, ErrDenied) || errors.Is(err, ErrClosed) || errors.Is(err, ErrCanceled) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrVersion)
}

func (c *Client) runUpload(t *Transfer, p *plan, remoteDir string) {
	select {
	case c.upSem <- struct{}{}:
		defer func() { <-c.upSem }()
	case <-t.ctx.Done():
		t.finish(context.Cause(t.ctx))
		return
	}
	ctx := t.ctx
	root := t.res.Name
	if p.isDir {
		r, err := c.request(ctx, msg{Op: "mkdir", Dir: remoteDir, Rel: root, Unique: true, Transfer: t.id}, nil)
		if err != nil {
			t.finish(err)
			return
		}
		root = r.Name
		t.mu.Lock()
		t.res.Name = root
		t.mu.Unlock()
		for _, d := range p.dirs {
			rel := root + d[strings.IndexByte(d, '/'):]
			if _, err := c.request(ctx, msg{Op: "mkdir", Dir: remoteDir, Rel: rel, Transfer: t.id}, nil); err != nil {
				if fatal(err) {
					t.finish(err)
					return
				}
				t.mu.Lock()
				t.addFailure(d, err)
				t.mu.Unlock()
			}
		}
	}
	buf := make([]byte, headerLen+ChunkSize)
	for _, f := range p.files {
		rel := f.rel
		if p.isDir {
			rel = root + f.rel[strings.IndexByte(f.rel, '/'):]
		}
		t.meter.update(true, func(pr *Progress) { pr.Name = filepath.Base(f.abs) })
		name, err := c.uploadFile(t, f, rel, remoteDir, buf)
		if err != nil && fatal(err) {
			t.finish(err)
			return
		}
		t.mu.Lock()
		if err != nil {
			t.addFailure(f.rel, err)
		} else {
			t.res.Files++
			t.res.Bytes += f.size
			if !p.isDir {
				t.res.Name = name
			}
		}
		t.mu.Unlock()
		t.meter.update(true, func(pr *Progress) { pr.FilesDone++ })
	}
	t.finish(nil)
}

// uploadFile sends one file and returns its final name on the host.
func (c *Client) uploadFile(t *Transfer, f planFile, rel, remoteDir string, buf []byte) (string, error) {
	ctx := t.ctx
	src, err := os.Open(f.abs)
	if err != nil {
		return "", localError(err)
	}
	defer src.Close()
	if _, err := c.request(ctx, msg{Op: "upload_begin", Transfer: t.id, Dir: remoteDir, Rel: rel, Size: f.size, Mode: f.mode, MTime: f.mtime}, nil); err != nil {
		return "", err
	}
	sum, err := sendFile(ctx, c.ep, t.id, src, f.size, buf, func() error {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.failed[rel] {
			return errHostWrite
		}
		return nil
	}, func(n int) { t.meter.update(false, func(p *Progress) { p.Done += int64(n) }) })
	if err != nil && fatal(err) {
		return "", err
	}
	end := msg{Op: "end", Transfer: t.id, Rel: rel, SHA256: sum, Size: f.size}
	if err != nil && err != errHostWrite {
		end.Error, end.Detail = codeOf(err), describe(err)
	}
	r, rerr := c.request(ctx, end, nil)
	if rerr != nil {
		return "", rerr
	}
	if err != nil {
		return "", err
	}
	return r.Name, nil
}

var errHostWrite = errors.New("files: host failed to write the file")
