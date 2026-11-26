package session

import (
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/files"

	"github.com/tangoui/kkdesk/internal/pairing"
)

// Mode is what a connection is for, chosen by the controller.
type Mode string

const (
	// ModeDesktop is remote control: the host captures and streams its
	// screen. It is the default.
	ModeDesktop Mode = "desktop"
	// ModeFiles only transfers files: the host must not start capture.
	ModeFiles Mode = "files"
)

// DialOptions configures DialWithOptions.
type DialOptions struct {
	ExpectedHostKey string
	Mode            Mode // "" means ModeDesktop
	// Identity, if set, is proved to the host so it can recognize a
	// controller it has let in before (its password pause then does not
	// apply to us).
	Identity *pairing.Store
}

func normMode(m Mode) Mode {
	if m == ModeFiles {
		return ModeFiles
	}
	return ModeDesktop
}

// Mode returns the connection mode the controller asked for.
func (p *Peer) Mode() Mode { return p.mode }

// FileChannel returns the ordered, reliable "files" data channel for
// files.NewClient (controller) or files.NewServer (host). Messages that
// arrive before a handler is registered are queued.
func (p *Peer) FileChannel() files.Channel {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.files == nil {
		return nil
	}
	return p.files
}

// maxQueued bounds messages held before the files engine is attached.
const maxQueued = 1024

// fileChannel adapts a *webrtc.DataChannel to files.Channel.
type fileChannel struct {
	dc *webrtc.DataChannel

	dmu   sync.Mutex // serializes delivery and handler registration
	onMsg func(bool, []byte)
	queue []webrtc.DataChannelMessage

	cmu       sync.Mutex
	onClose   func()
	closed    bool
	closeOnce sync.Once
}

var _ files.Channel = (*fileChannel)(nil)

func newFileChannel(dc *webrtc.DataChannel, blocked func() bool) *fileChannel {
	f := &fileChannel{dc: dc}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !blocked() {
			f.deliver(msg)
		}
	})
	return f
}

func (f *fileChannel) deliver(m webrtc.DataChannelMessage) {
	f.dmu.Lock()
	defer f.dmu.Unlock()
	if f.onMsg == nil {
		if len(f.queue) < maxQueued {
			f.queue = append(f.queue, m)
		}
		return
	}
	f.onMsg(m.IsString, m.Data)
}

func (f *fileChannel) SendText(s string) error { return f.dc.SendText(s) }
func (f *fileChannel) Send(b []byte) error     { return f.dc.Send(b) }
func (f *fileChannel) BufferedAmount() uint64  { return f.dc.BufferedAmount() }
func (f *fileChannel) SetBufferedAmountLowThreshold(n uint64) {
	f.dc.SetBufferedAmountLowThreshold(n)
}
func (f *fileChannel) OnBufferedAmountLow(cb func()) { f.dc.OnBufferedAmountLow(cb) }

func (f *fileChannel) OnMessage(cb func(isText bool, data []byte)) {
	f.dmu.Lock()
	defer f.dmu.Unlock()
	f.onMsg = cb
	q := f.queue
	f.queue = nil
	for _, m := range q {
		cb(m.IsString, m.Data)
	}
}

// OnClose registers cb, called once when the channel or the whole session
// closes (immediately, in the background, if it already has).
func (f *fileChannel) OnClose(cb func()) {
	f.cmu.Lock()
	f.onClose = cb
	closed := f.closed
	f.cmu.Unlock()
	if closed {
		go f.fireClose()
	}
}

func (f *fileChannel) markClosed() {
	f.cmu.Lock()
	f.closed = true
	f.cmu.Unlock()
	f.fireClose()
}

func (f *fileChannel) fireClose() {
	f.cmu.Lock()
	cb := f.onClose
	f.cmu.Unlock()
	if cb != nil {
		f.closeOnce.Do(cb)
	}
}
