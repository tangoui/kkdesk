package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Progress reports a transfer's state. Name is the file being transferred.
type Progress struct {
	Name             string
	Done, Total      int64 // bytes
	Files, FilesDone int
	BytesPerSec      float64
}

// FileError is one file that failed inside a folder transfer.
type FileError struct {
	Path string // "/"-separated path relative to the transfer's parent dir
	Err  error
}

// Result summarizes a finished transfer.
type Result struct {
	// Name is the final name of the top-level file or folder at the
	// destination, after conflict renaming ("a (1).txt").
	Name    string
	Files   int   // files written successfully
	Bytes   int64 // bytes of successfully written files
	Elapsed time.Duration
	Skipped []string // symbolic links and special files, not transferred
	Failed  []FileError
}

// PartialError is returned by Wait when some files of a transfer failed.
type PartialError struct {
	Failed, Total int
	First         error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("files: %d of %d files failed (first: %v)", e.Failed, e.Total, e.First)
}

func (e *PartialError) Unwrap() error { return e.First }

// incoming writes one received file to a temporary file next to its
// destination and renames it into place once its size and SHA-256 check out.
type incoming struct {
	mu          sync.Mutex
	rel         string // as sent by the other side
	reported    bool   // write error sent to the other side (server message goroutine)
	dir, name   string // destination directory and wanted (sanitized) name
	tmp         string
	f           *os.File
	size, wrote int64
	h           hash.Hash
	mode        uint32
	mtime       int64
	err         error // first write error; the rest of the file is ignored
	started     time.Time
}

func newIncoming(dir, name string, size int64, mode uint32, mtime int64) (*incoming, error) {
	for i := 0; i < 1000; i++ {
		tmp := "." + name
		if i > 0 {
			tmp += "." + strconv.Itoa(i)
		}
		tmp = filepath.Join(dir, tmp+partSuffix)
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &incoming{dir: dir, name: name, tmp: tmp, f: f, size: size, h: sha256.New(), mode: mode, mtime: mtime, started: time.Now()}, nil
	}
	return nil, &RemoteError{Code: codeIO, Detail: "too many temporary files for " + name}
}

func (in *incoming) write(off int64, data []byte) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.f == nil && in.err == nil {
		in.err = ErrCanceled // aborted
	}
	if in.err != nil {
		return in.err
	}
	switch {
	case off != in.wrote:
		in.err = &RemoteError{Code: codeInvalid, Detail: fmt.Sprintf("chunk at offset %d, expected %d", off, in.wrote)}
	case in.wrote+int64(len(data)) > in.size:
		in.err = &RemoteError{Code: codeChecksum, Detail: "more data than announced"}
	default:
		if _, err := in.f.Write(data); err != nil {
			in.err = localError(err)
		} else {
			in.h.Write(data)
			in.wrote += int64(len(data))
		}
	}
	return in.err
}

// abort closes and removes the temporary file.
func (in *incoming) abort() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.abortLocked()
}

func (in *incoming) abortLocked() {
	if in.f != nil {
		in.f.Close()
		in.f = nil
	}
	os.Remove(in.tmp)
}

// finish verifies the file and moves it into place without overwriting
// anything, returning the final name.
func (in *incoming) finish(sum string) (string, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	err := in.err
	if err == nil && in.f == nil {
		err = ErrCanceled
	}
	if err == nil {
		err = in.f.Close()
		in.f = nil
		if err != nil {
			err = localError(err)
		}
	}
	if err == nil && (in.wrote != in.size || hex.EncodeToString(in.h.Sum(nil)) != sum) {
		err = &RemoteError{Code: codeChecksum, Detail: fmt.Sprintf("%s: received %d of %d bytes or SHA-256 differs", in.name, in.wrote, in.size)}
	}
	if err != nil {
		in.abortLocked()
		return "", err
	}
	if in.mtime != 0 {
		t := time.Unix(0, in.mtime)
		_ = os.Chtimes(in.tmp, t, t) // best effort
	}
	name, err := placeFile(in.tmp, in.dir, in.name)
	if err != nil {
		in.abortLocked()
		return "", localError(err)
	}
	if in.mode&0o777 != 0 {
		_ = os.Chmod(filepath.Join(in.dir, name), os.FileMode(in.mode&0o777)) // best effort
	}
	return name, nil
}

// placeFile moves tmp to dir under name or the first free "name (n)". A hard
// link refuses to replace an existing file; file systems without hard links
// fall back to a checked rename.
func placeFile(tmp, dir, name string) (string, error) {
	for i := 0; i < 10000; i++ {
		n := uniqueName(dir, name)
		dst := filepath.Join(dir, n)
		err := os.Link(tmp, dst)
		if err == nil {
			os.Remove(tmp)
			return n, nil
		}
		if errors.Is(err, os.ErrExist) {
			continue // lost a race; pick another name
		}
		if exists(dst) {
			continue
		}
		if err := os.Rename(tmp, dst); err != nil {
			return "", err
		}
		return n, nil
	}
	return "", &RemoteError{Code: codeExists, Detail: name}
}

// sendFile streams size bytes of r as data chunks of transfer id, honouring
// flow control. check is called before every chunk; a non-nil result stops
// the file. It returns the SHA-256 of the bytes sent.
func sendFile(ctx context.Context, ep *endpoint, id uint64, r io.Reader, size int64, buf []byte, check func() error, sent func(int)) (string, error) {
	h := sha256.New()
	var off int64
	for off < size {
		if err := check(); err != nil {
			return "", err
		}
		if err := ep.waitFlow(ctx); err != nil {
			return "", err
		}
		n := int(min(int64(ChunkSize), size-off))
		if _, err := io.ReadFull(r, buf[headerLen:headerLen+n]); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return "", &RemoteError{Code: codeIO, Detail: "file shrank while it was being read"}
			}
			return "", localError(err)
		}
		putHeader(buf, kindData, id, off)
		if err := ep.ch.Send(buf[:headerLen+n]); err != nil {
			return "", ep.sendErr(err)
		}
		h.Write(buf[headerLen : headerLen+n])
		off += int64(n)
		sent(n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// meter tracks progress and a smoothed transfer rate.
type meter struct {
	mu       sync.Mutex
	p        Progress
	cb       func(Progress)
	start    time.Time
	lastAt   time.Time
	lastDone int64
	lastEmit time.Time
}

func newMeter(cb func(Progress)) *meter {
	now := time.Now()
	return &meter{cb: cb, start: now, lastAt: now}
}

// update applies f to the progress and reports it, at most every 100 ms
// unless force is set. Callbacks for one transfer come from one goroutine at
// a time.
func (m *meter) update(force bool, f func(p *Progress)) {
	m.mu.Lock()
	f(&m.p)
	now := time.Now()
	if dt := now.Sub(m.lastAt); dt >= 250*time.Millisecond {
		inst := float64(m.p.Done-m.lastDone) / dt.Seconds()
		if m.p.BytesPerSec == 0 {
			m.p.BytesPerSec = inst
		} else {
			m.p.BytesPerSec = 0.6*m.p.BytesPerSec + 0.4*inst
		}
		m.lastAt, m.lastDone = now, m.p.Done
	}
	if m.cb == nil || (!force && now.Sub(m.lastEmit) < 100*time.Millisecond) {
		m.mu.Unlock()
		return
	}
	m.lastEmit = now
	p := m.p
	m.mu.Unlock()
	m.cb(p)
}

func (m *meter) snapshot() Progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.p
}

// Transfer is one download or upload of a file or folder.
type Transfer struct {
	id     uint64
	c      *Client
	upload bool
	ctx    context.Context
	cancel context.CancelCauseFunc
	meter  *meter

	mu   sync.Mutex
	res  Result
	err  error
	done chan struct{}
	fin  bool

	// download receiver state, guarded by mu
	localDir string
	root     string // final name of the top-level folder
	cur      *incoming
	curRel   string
	failed   map[string]bool
}

func (t *Transfer) addFailure(rel string, err error) {
	t.res.Failed = append(t.res.Failed, FileError{Path: rel, Err: err})
}

// Cancel stops the transfer on both sides and removes partial files.
func (t *Transfer) Cancel() {
	if t.finish(ErrCanceled) {
		t.c.sendCancel(t.id)
	}
}

// Done is closed when the transfer has finished, failed or been canceled.
func (t *Transfer) Done() <-chan struct{} { return t.done }

// Progress returns the latest progress.
func (t *Transfer) Progress() Progress { return t.meter.snapshot() }

// Wait blocks until the transfer ends. The error is nil if every file
// arrived, a *PartialError if some failed (see Result.Failed), or the reason
// the whole transfer stopped (ErrCanceled, ErrDenied, ErrClosed, ...).
func (t *Transfer) Wait() (Result, error) {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.res, t.err
}

// finish ends the transfer once: err == nil means it ran to completion
// (partial failures are derived from Result.Failed). It removes a partial
// download, and reports whether this call ended it.
func (t *Transfer) finish(err error) bool {
	t.mu.Lock()
	if t.fin {
		t.mu.Unlock()
		return false
	}
	t.fin = true
	if t.cur != nil {
		t.cur.abort()
		t.cur = nil
	}
	t.res.Elapsed = time.Since(t.meter.start)
	if err == nil && len(t.res.Failed) > 0 {
		total := t.meter.snapshot().Files
		if total == 1 && len(t.res.Failed) == 1 {
			err = t.res.Failed[0].Err
		} else {
			err = &PartialError{Failed: len(t.res.Failed), Total: total, First: t.res.Failed[0].Err}
		}
	}
	t.err = err
	res := t.res
	t.mu.Unlock()

	t.cancel(errOr(err, ErrCanceled))
	t.c.forget(t.id)
	dir := "download"
	if t.upload {
		dir = "upload"
	}
	mbps := 0.0
	if s := res.Elapsed.Seconds(); s > 0 {
		mbps = float64(res.Bytes) / s / 1e6
	}
	t.c.log("files: %s %q: %d files, %d bytes in %v (%.1f MB/s), err=%v",
		dir, res.Name, res.Files, res.Bytes, res.Elapsed.Round(time.Millisecond), mbps, err)
	close(t.done)
	return true
}

func errOr(err, def error) error {
	if err == nil {
		return def
	}
	return err
}
