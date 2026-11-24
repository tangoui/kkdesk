package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tangoui/kkdesk/internal/codec"
	"github.com/tangoui/kkdesk/internal/display"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/session"
	"github.com/tangoui/kkdesk/internal/stats"
)

// DialTimeout bounds connecting, authenticating and negotiating.
const DialTimeout = 15 * time.Second

// ViewerConfig configures a Viewer.
type ViewerConfig struct {
	Addr     string // host[:port]; the port defaults to 21118
	Password string
	ViewOnly bool // force view-only
	Log      io.Writer
}

// ViewerPhase is the viewer's connection state.
type ViewerPhase int

const (
	ViewerConnecting ViewerPhase = iota // dialing
	ViewerWaiting                       // connected, no picture yet (host confirming)
	ViewerStreaming                     // showing video
	ViewerClosed                        // ended; see Err
)

// ViewerState is a snapshot for the UI.
type ViewerState struct {
	Phase      ViewerPhase
	Addr       string
	Err        string // why it ended, "" for a normal exit
	ViewOnly   bool
	BitrateBps int // 0 = auto
	FPS        int
	Stats      stats.Second // latest per-second aggregate
}

// Viewer connects to a host and shows its screen. Methods are safe for
// concurrent use.
type Viewer struct {
	cfg       ViewerConfig
	notifyFns atomic.Pointer[[]func()]
	rec       *stats.Recorder

	mu       sync.Mutex
	phase    ViewerPhase
	errMsg   string
	peer     *session.Peer
	view     *display.View
	viewOnly bool
	bitrate  int
	fps      int
	closing  bool
	onEnd    []func()

	// video goroutine only
	dec      codec.Decoder
	recvAt   time.Time
	lastPLI  time.Time
	seq      uint64
	gotFrame atomic.Bool
}

// NewViewer returns a viewer for cfg. notify is called from any goroutine
// when the state changes.
func NewViewer(cfg ViewerConfig, notify func()) *Viewer {
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		cfg.Addr = net.JoinHostPort(cfg.Addr, fmt.Sprint(Port))
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	v := &Viewer{cfg: cfg, viewOnly: cfg.ViewOnly || IsLoopback(cfg.Addr), fps: 60}
	if notify != nil {
		v.AddNotify(notify)
	}
	return v
}

// AddNotify adds a function called (from any goroutine) on state changes,
// such as a window's Invalidate.
func (v *Viewer) AddNotify(f func()) {
	for {
		old := v.notifyFns.Load()
		var fns []func()
		if old != nil {
			fns = append(fns, *old...)
		}
		fns = append(fns, f)
		if v.notifyFns.CompareAndSwap(old, &fns) {
			return
		}
	}
}

func (v *Viewer) notify() {
	if fns := v.notifyFns.Load(); fns != nil {
		for _, f := range *fns {
			f()
		}
	}
}

// IsLoopback reports whether addr (host[:port]) names this machine's
// loopback interface; self-connections default to view-only so the
// injected input does not fight the local mouse.
func IsLoopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		h = addr
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Addr returns the host address.
func (v *Viewer) Addr() string { return v.cfg.Addr }

// Connect dials the host. It blocks until the session is established or
// fails; on failure the state is ViewerClosed with a user-facing Err.
func (v *Viewer) Connect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	p, err := session.Dial(ctx, v.cfg.Addr, v.cfg.Password)
	if err != nil {
		msg := DialError(v.cfg.Addr, err)
		v.end(msg)
		return errors.New(msg)
	}
	v.rec = stats.NewRecorder(v.cfg.Log, "viewer", stats.ProcessCPU("VTDecoderXPCService", "WindowServer"))
	v.rec.Start()
	v.mu.Lock()
	if v.closing {
		v.mu.Unlock()
		p.Close()
		v.end("")
		return errors.New("已取消")
	}
	v.peer, v.phase = p, ViewerWaiting
	v.mu.Unlock()
	p.OnVideo(v.video)
	go v.watch(p)
	v.notify()
	return nil
}

// DialError turns a Dial error into a message for the user.
func DialError(addr string, err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, session.ErrBadPassword):
		return "密码错误"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return fmt.Sprintf("连接 %s 超时", addr)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Sprintf("无法连接到 %s：对方没有开启远程控制", addr)
	case errors.As(err, new(*net.OpError)):
		return fmt.Sprintf("无法连接到 %s：%v", addr, err)
	}
	return fmt.Sprintf("连接失败：%v", err)
}

func (v *Viewer) watch(p *session.Peer) {
	<-p.Done()
	msg := p.Reason()
	v.mu.Lock()
	closing := v.closing
	v.mu.Unlock()
	if msg == "" && !closing {
		msg = "连接已断开"
	}
	v.end(msg)
}

// end moves to ViewerClosed once and runs the OnEnd callbacks.
func (v *Viewer) end(msg string) {
	v.mu.Lock()
	if v.phase == ViewerClosed {
		v.mu.Unlock()
		return
	}
	v.phase, v.errMsg = ViewerClosed, msg
	fns := v.onEnd
	p := v.peer
	v.mu.Unlock()
	if p != nil {
		p.Close()
		<-p.Done()
	}
	if v.rec != nil {
		v.rec.Close()
	}
	v.notify()
	for _, f := range fns {
		f()
	}
}

// OnEnd registers f to run (on some goroutine) when the session ends.
func (v *Viewer) OnEnd(f func()) {
	v.mu.Lock()
	if v.phase == ViewerClosed {
		v.mu.Unlock()
		f()
		return
	}
	v.onEnd = append(v.onEnd, f)
	v.mu.Unlock()
}

// Close ends the session (退出远控).
func (v *Viewer) Close() {
	v.mu.Lock()
	v.closing = true
	p := v.peer
	v.mu.Unlock()
	if p != nil {
		p.Close()
	}
}

// State returns a snapshot.
func (v *Viewer) State() ViewerState {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := ViewerState{Phase: v.phase, Addr: v.cfg.Addr, Err: v.errMsg, ViewOnly: v.viewOnly, BitrateBps: v.bitrate, FPS: v.fps}
	if v.rec != nil {
		s.Stats = v.rec.Last()
	}
	return s
}

// SetView attaches the display view (nil detaches it before release).
func (v *Viewer) SetView(view *display.View) {
	v.mu.Lock()
	v.view = view
	v.mu.Unlock()
	if view != nil {
		v.requestKeyframe() // the view missed earlier frames
	}
}

// View returns the attached display view.
func (v *Viewer) View() *display.View {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.view
}

// FrameSize returns the size of the latest displayed frame.
func (v *Viewer) FrameSize() (w, h int) {
	v.mu.Lock()
	view := v.view
	v.mu.Unlock()
	if view == nil {
		return 0, 0
	}
	return view.FrameSize()
}

// SetViewOnly turns input forwarding off (true) or on.
func (v *Viewer) SetViewOnly(on bool) {
	v.mu.Lock()
	v.viewOnly = on
	v.mu.Unlock()
	v.notify()
}

// SetQuality asks the host for a bitrate (0 = auto) and frame rate.
func (v *Viewer) SetQuality(bitrateBps, fps int) {
	v.mu.Lock()
	v.bitrate, v.fps = bitrateBps, fps
	p := v.peer
	v.mu.Unlock()
	if p != nil {
		if err := p.SendQuality(session.Quality{BitrateBps: bitrateBps, FPS: fps}); err != nil {
			log.Printf("kkdesk viewer: quality: %v", err)
		}
	}
	v.notify()
}

// Send forwards one input event unless view-only.
func (v *Viewer) Send(e input.Event) {
	v.mu.Lock()
	p, off := v.peer, v.viewOnly
	v.mu.Unlock()
	if p == nil || off {
		return
	}
	_ = p.SendInput(e)
}

// ViewOnly reports whether input forwarding is off.
func (v *Viewer) ViewOnly() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.viewOnly
}

func (v *Viewer) requestKeyframe() {
	v.mu.Lock()
	p := v.peer
	v.mu.Unlock()
	if p != nil {
		_ = p.RequestKeyframe()
	}
}

// video runs on the session's single video goroutine.
func (v *Viewer) video(frame []byte, captureAt time.Time) {
	v.recvAt = time.Now()
	v.rec.AddBytes(len(frame))
	if v.dec == nil {
		d, err := codec.NewDecoder(v.decoded)
		if err != nil {
			log.Printf("kkdesk viewer: decoder: %v", err)
			v.Close()
			return
		}
		v.dec = d
		go func() { <-v.peerDone(); d.Close() }()
	}
	if err := v.dec.Decode(frame, captureAt); err != nil {
		if time.Since(v.lastPLI) > 500*time.Millisecond {
			v.lastPLI = time.Now()
			v.requestKeyframe()
		}
	}
}

func (v *Viewer) peerDone() <-chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.peer.Done()
}

// decoded runs inside Decode, on the decoder's thread.
func (v *Viewer) decoded(pix uintptr, captureAt time.Time) {
	decodedAt := time.Now()
	v.mu.Lock()
	view, peer := v.view, v.peer
	v.mu.Unlock()
	if view == nil {
		return
	}
	enq, err := view.Enqueue(pix, captureAt)
	if err != nil {
		return
	}
	v.seq++
	// captureAt is on the host's clock; stats converts it with the offset.
	clk := stats.Clock{Remote: true}
	if peer != nil {
		clk.Offset, clk.RTT, clk.Synced = peer.ClockOffset()
	}
	v.rec.Frame(stats.FrameTimes{
		Seq: v.seq, Capture: captureAt, Received: v.recvAt, Decoded: decodedAt,
		Enqueued: enq, Presented: view.NextVsync(enq), Clock: clk,
	})
	if !v.gotFrame.Swap(true) {
		v.mu.Lock()
		if v.phase == ViewerWaiting {
			v.phase = ViewerStreaming
		}
		v.mu.Unlock()
		v.notify()
	}
}
