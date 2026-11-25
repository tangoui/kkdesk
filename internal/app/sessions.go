package app

import (
	"slices"
	"sync"
	"time"
)

// Runtime data of this process for the 远程会话 and 文件传输 pages: the
// sessions this machine started (one Viewer each) and the transfers of this
// run. Nothing here is saved.

// SessionInfo is a snapshot of one outgoing session.
type SessionInfo struct {
	Viewer    *Viewer
	Addr      string
	Since     time.Time // connected at
	FilesOnly bool      // 传文件
	State     ViewerState
}

// TransferRecord is one finished transfer of the file window.
type TransferRecord struct {
	At      time.Time // finished at
	Peer    string    // the other side's address
	Name    string    // file or folder name
	Upload  bool      // this machine → peer
	Bytes   int64     // bytes written
	Files   int
	Elapsed time.Duration
	Result  TransferResult
	Err     string // why it failed
}

// TransferResult is how a transfer ended.
type TransferResult int

const (
	TransferDone TransferResult = iota
	TransferFailed
	TransferCanceled
)

// maxHistory bounds the transfer history.
const maxHistory = 200

var runtimeData struct {
	mu       sync.Mutex
	sessions []*Viewer
	history  []TransferRecord // newest first
	watchers []func()
}

// Watch registers f, called (from any goroutine) when the sessions or the
// transfer history change.
func Watch(f func()) {
	runtimeData.mu.Lock()
	runtimeData.watchers = append(runtimeData.watchers, f)
	runtimeData.mu.Unlock()
}

func changed() {
	runtimeData.mu.Lock()
	ws := slices.Clone(runtimeData.watchers)
	runtimeData.mu.Unlock()
	for _, f := range ws {
		f()
	}
}

func addSession(v *Viewer) {
	runtimeData.mu.Lock()
	runtimeData.sessions = append(runtimeData.sessions, v)
	runtimeData.mu.Unlock()
	v.AddNotify(changed)
	changed()
}

func removeSession(v *Viewer) {
	runtimeData.mu.Lock()
	i := slices.Index(runtimeData.sessions, v)
	if i >= 0 {
		runtimeData.sessions = slices.Delete(runtimeData.sessions, i, i+1)
	}
	runtimeData.mu.Unlock()
	if i >= 0 {
		changed()
	}
}

// Sessions returns the connected outgoing sessions, oldest first.
func Sessions() []SessionInfo {
	runtimeData.mu.Lock()
	vs := slices.Clone(runtimeData.sessions)
	runtimeData.mu.Unlock()
	out := make([]SessionInfo, 0, len(vs))
	for _, v := range vs {
		v.mu.Lock()
		since := v.since
		v.mu.Unlock()
		out = append(out, SessionInfo{Viewer: v, Addr: v.Addr(), Since: since, FilesOnly: v.FilesOnly(), State: v.State()})
	}
	return out
}

// RecordTransfer adds a finished transfer to the history.
func RecordTransfer(r TransferRecord) {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	runtimeData.mu.Lock()
	runtimeData.history = slices.Insert(runtimeData.history, 0, r)
	if len(runtimeData.history) > maxHistory {
		runtimeData.history = runtimeData.history[:maxHistory]
	}
	runtimeData.mu.Unlock()
	changed()
}

// TransferHistory returns the transfers of this run, newest first.
func TransferHistory() []TransferRecord {
	runtimeData.mu.Lock()
	defer runtimeData.mu.Unlock()
	return slices.Clone(runtimeData.history)
}

// ClearTransferHistory empties the history.
func ClearTransferHistory() {
	runtimeData.mu.Lock()
	runtimeData.history = nil
	runtimeData.mu.Unlock()
	changed()
}
