package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tangoui/kkdesk/internal/clipsync"
	"github.com/tangoui/kkdesk/internal/codec"
	"github.com/tangoui/kkdesk/internal/display"
	"github.com/tangoui/kkdesk/internal/files"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/rendezvous"
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
	// FilesOnly connects only to transfer files (session.ModeFiles): the
	// host shares no screen.
	FilesOnly bool
	Log       io.Writer
	// FPS and BitrateBps are the quality the session asks for once
	// connected (设置 › 画质); 0 FPS leaves the host's frame rate and 0
	// BitrateBps is automatic.
	FPS        int
	BitrateBps int
	Remote     rendezvous.Config
	// Clipboard is the local clipboard, synced with the host's while
	// controlling (not view-only, not files-only); nil disables syncing.
	Clipboard clipsync.Clipboard
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
	Phase               ViewerPhase
	Addr                string
	Err                 string // why it ended, "" for a normal exit
	ViewOnly            bool
	AccessibilityDenied bool
	BitrateBps          int // 0 = auto
	FPS                 int
	Stats               stats.Second // latest per-second aggregate
	Relayed             bool
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
	relayed  bool
	onEnd    []func()
	files    *files.Client
	since    time.Time // connected at

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
	if rendezvous.IsCode(cfg.Addr) {
		cfg.Addr = rendezvous.NormalizeCode(cfg.Addr)
	} else if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		cfg.Addr = net.JoinHostPort(cfg.Addr, fmt.Sprint(Port))
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	v := &Viewer{cfg: cfg, viewOnly: cfg.ViewOnly || IsLoopback(cfg.Addr), fps: 60, bitrate: cfg.BitrateBps}
	if cfg.FPS > 0 {
		v.fps = cfg.FPS
	}
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

// FilesOnly reports whether the session only transfers files.
func (v *Viewer) FilesOnly() bool { return v.cfg.FilesOnly }

// Files returns the session's file client (nil before Connect succeeded or
// if the host offers no file channel). It is created once per session.
func (v *Viewer) Files() *files.Client {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.files == nil && v.peer != nil {
		if ch := v.peer.FileChannel(); ch != nil {
			v.files = files.NewClient(ch)
			v.files.SetLogf(func(format string, args ...any) { log.Printf("kkdesk viewer: "+format, args...) })
		}
	}
	return v.files
}

// Connect dials the host. It blocks until the session is established or
// fails; on failure the state is ViewerClosed with a user-facing Err.
func (v *Viewer) Connect(ctx context.Context) error {
	isRemote := rendezvous.IsCode(v.cfg.Addr)
	dialTimeout := DialTimeout
	if isRemote {
		dialTimeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	mode := session.ModeDesktop
	if v.cfg.FilesOnly {
		mode = session.ModeFiles
	}
	var p *session.Peer
	var err error
	if isRemote {
		if v.cfg.Remote.URL == "" {
			err = errors.New("请先在「设置 › 网络」配置跨网服务")
		} else {
			p, err = session.DialRemote(ctx, v.cfg.Remote, v.cfg.Addr, mode)
		}
	} else {
		p, err = session.DialWithOptions(ctx, v.cfg.Addr, v.cfg.Password, session.DialOptions{Mode: mode})
	}
	if err != nil {
		msg := DialError(v.cfg.Addr, err)
		if isRemote {
			msg = fmt.Sprintf("跨网连接失败：%v", err)
		}
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
	v.peer, v.phase, v.since = p, ViewerWaiting, time.Now()
	v.relayed = isRemote && p.Relayed()
	fps, bps := v.fps, v.bitrate
	v.mu.Unlock()
	p.OnAccessibilityChange(v.notify)
	p.OnVideo(v.video)
	go v.watch(p)
	addSession(v)
	if !v.cfg.FilesOnly && (v.cfg.FPS > 0 || v.cfg.BitrateBps > 0) {
		v.SetQuality(bps, fps) // the host applies it once it streams
	}
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
	case unreachable(err):
		// The usual cause: the two machines sit behind different routers
		// (or a guest network), so this one has no route to the other.
		msg := fmt.Sprintf("无法连接到 %s：网络不通，两台电脑可能不在同一个局域网", addr)
		if ips := LocalIPv4(); len(ips) > 0 {
			msg += "（本机是 " + strings.Join(ips, "、") + "）"
		}
		return msg + "。请把两台电脑接到同一个路由器或 Wi-Fi，或从对方连接本机"
	case errors.As(err, new(*net.OpError)):
		return fmt.Sprintf("无法连接到 %s：%v", addr, err)
	}
	return fmt.Sprintf("连接失败：%v", err)
}

// unreachable reports a dial error caused by having no route to the host
// or its network (EHOSTUNREACH, ENETUNREACH; WSAEHOSTUNREACH 10065 and
// WSAENETUNREACH 10051 on Windows).
func unreachable(err error) bool {
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == 10065 || errno == 10051)
}

// syncClipboard keeps this clipboard and the host's in step while
// controlling; the returned function stops it. The host has the final say
// (its 剪贴板 permission).
func (v *Viewer) syncClipboard(p *session.Peer) (stop func()) {
	if v.cfg.Clipboard == nil || v.cfg.FilesOnly {
		return func() {}
	}
	cs := clipsync.Start(clipsync.Options{
		Local:   v.cfg.Clipboard,
		Send:    p.SendClipboard,
		Allowed: func() bool { return !v.ViewOnly() },
		MaxLen:  session.MaxClipboard,
		Logf:    func(format string, args ...any) { log.Printf("kkdesk viewer: "+format, args...) },
	})
	p.OnClipboard(cs.Receive)
	return func() { p.OnClipboard(nil); cs.Stop() }
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
	removeSession(v)
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
	s := ViewerState{Phase: v.phase, Addr: v.cfg.Addr, Err: v.errMsg, ViewOnly: v.viewOnly, BitrateBps: v.bitrate, FPS: v.fps, Relayed: v.relayed}
	if v.peer != nil {
		s.AccessibilityDenied = v.peer.AccessibilityDenied()
	}
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
	if p == nil || off || p.AccessibilityDenied() {
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
		// The first frame means the host accepted and streams: start the
		// clipboard sync now, so a copy made while it was still confirming
		// is not lost (the host drops clipboard text until it accepts).
		go func() {
			v.mu.Lock()
			p := v.peer
			v.mu.Unlock()
			stop := v.syncClipboard(p)
			<-p.Done()
			stop()
		}()
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
