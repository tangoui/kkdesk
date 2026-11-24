// Package app wires the kkdesk pipelines: the host shares this screen
// (capture → encode → session, session input → inject) and the viewer shows
// a remote one (session → decode → display, UI input → session). It holds no
// UI; package ui renders the state these types expose.
package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/codec"
	"github.com/tangoui/kkdesk/internal/inject"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/session"
	"github.com/tangoui/kkdesk/internal/stats"
)

// Port is the default signaling port.
const Port = 21118

// ConfirmTimeout is how long an incoming connection waits for the user.
const ConfirmTimeout = 30 * time.Second

// AutoBitrate is the bitrate of the automatic quality level. The session has
// no bandwidth estimator yet (pion's GCC interceptor is not registered), so
// "auto" is a fixed 8 Mbps for now. TODO: follow a bandwidth estimate.
const AutoBitrate = 8_000_000

// HostConfig configures a Host.
type HostConfig struct {
	Addr        string // listen address; default ":21118"
	Password    string // fixed password; empty generates a 6-digit one
	FPS         int    // capture frame rate
	MaxWidth    int    // capture width limit; 0 = native
	BitrateMbps int    // 0 = auto
	Log         io.Writer
	AutoAccept  bool
}

// HostPhase is what the host is doing.
type HostPhase int

const (
	HostOff       HostPhase = iota // not listening
	HostListening                  // waiting for a controller
	HostPending                    // a controller waits for confirmation
	HostActive                     // being controlled
)

// HostState is a snapshot for the UI.
type HostState struct {
	Phase    HostPhase
	Addrs    []string // local IPv4 addresses
	Port     int
	Password string
	Peer     string    // remote address while pending or active
	Since    time.Time // active: session start; pending: request time
	Deadline time.Time // pending: automatic rejection
	Err      string    // last error or permission guidance
	Notice   string    // informational message (e.g. last session ended)
}

// Host accepts controllers and streams this screen to one at a time. Its
// methods are safe for concurrent use.
type Host struct {
	cfg    HostConfig
	notify func()

	mu       sync.Mutex
	password string
	ln       *session.Host
	cancel   context.CancelFunc
	phase    HostPhase
	peer     *session.Peer
	since    time.Time
	decide   chan bool
	errMsg   string
	notice   string
	busy     bool
	wg       sync.WaitGroup
	addrs    []string
	stopOnce sync.Once
}

// NewHost returns a host that is not listening yet (see SetEnabled). notify
// is called from any goroutine when the state changes.
func NewHost(cfg HostConfig, notify func()) *Host {
	if cfg.Addr == "" {
		cfg.Addr = fmt.Sprintf(":%d", Port)
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 60
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if notify == nil {
		notify = func() {}
	}
	pw := cfg.Password
	if pw == "" {
		pw = NewPassword()
	}
	return &Host{cfg: cfg, notify: notify, password: pw, addrs: LocalIPv4()}
}

// NewPassword returns a random 6-digit password.
func NewPassword() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// LocalIPv4 lists the IPv4 addresses of the interfaces that are up,
// excluding loopback.
func LocalIPv4() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

// ExcludeAppName is the application name whose windows capture hides: this
// process's executable name, which macOS reports as the app name of an
// unbundled binary, so every running instance (host and viewer) is hidden.
func ExcludeAppName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return "kkdesk"
}

// excludeSelfWindows reports whether capture should hide kkdesk's own
// windows. A ScreenCaptureKit filter that excludes applications makes
// WindowServer composite a second, filtered image of the display every
// frame: measured on a 4K/60 Hz M4 it dropped the whole screen (source app
// and capture) from 60 to ~50 fps and delayed encoder output by ~9 ms. So
// it is only used where it is needed: when the controller runs on this
// machine (the viewer window would otherwise mirror itself). env
// (KKDESK_EXCLUDE_SELF) "0" or "1" forces it off or on.
func excludeSelfWindows(remote string, local []string, env string) bool {
	switch env {
	case "0":
		return false
	case "1":
		return true
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, l := range local {
		if ip.Equal(net.ParseIP(l)) {
			return true
		}
	}
	return false
}

// State returns a snapshot.
func (h *Host) State() HostState {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := HostState{Phase: h.phase, Addrs: h.addrs, Port: Port, Password: h.password, Err: h.errMsg, Notice: h.notice}
	if tcp, err := net.ResolveTCPAddr("tcp", h.cfg.Addr); err == nil && tcp.Port != 0 {
		s.Port = tcp.Port
	}
	if h.peer != nil {
		s.Peer = h.peer.RemoteAddr()
		s.Since = h.since
		if h.phase == HostPending {
			s.Deadline = h.since.Add(ConfirmTimeout)
		}
	}
	return s
}

// SetEnabled starts or stops listening. Stopping does not end an active
// session.
func (h *Host) SetEnabled(on bool) error {
	h.mu.Lock()
	defer h.notify()
	defer h.mu.Unlock()
	if on == (h.ln != nil) {
		return nil
	}
	if !on {
		h.stopLocked()
		if !h.busy {
			h.phase = HostOff
		}
		return nil
	}
	return h.startLocked()
}

func (h *Host) startLocked() error {
	ln, err := session.Listen(h.cfg.Addr, h.password)
	if err != nil {
		h.errMsg = fmt.Sprintf("无法监听 %s：%v", h.cfg.Addr, err)
		return err
	}
	h.errMsg = ""
	ctx, cancel := context.WithCancel(context.Background())
	h.ln, h.cancel = ln, cancel
	if !h.busy {
		h.phase = HostListening
	}
	h.wg.Add(1)
	go h.acceptLoop(ctx, ln)
	return nil
}

func (h *Host) stopLocked() {
	if h.ln == nil {
		return
	}
	h.cancel()
	h.ln.Close()
	h.ln, h.cancel = nil, nil
}

// ListenAddr returns the actual listening address, or "" when off.
func (h *Host) ListenAddr() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ln == nil {
		return ""
	}
	return h.ln.Addr().String()
}

// RefreshPassword generates a new password and restarts the listener with
// it. An active session is not affected.
func (h *Host) RefreshPassword() {
	h.mu.Lock()
	defer h.notify()
	defer h.mu.Unlock()
	h.password = NewPassword()
	if h.ln != nil {
		h.stopLocked()
		h.startLocked()
	}
}

// Accept accepts the pending connection.
func (h *Host) Accept() { h.answer(true) }

// Reject rejects the pending connection.
func (h *Host) Reject() { h.answer(false) }

func (h *Host) answer(ok bool) {
	h.mu.Lock()
	ch := h.decide
	h.decide = nil
	h.mu.Unlock()
	if ch != nil {
		ch <- ok
	}
}

// Disconnect ends the active (or pending) session.
func (h *Host) Disconnect() {
	h.mu.Lock()
	p := h.peer
	h.mu.Unlock()
	if p != nil {
		p.CloseWithReason("被控端已断开连接")
	}
}

// Close stops listening and ends any session.
func (h *Host) Close() {
	h.stopOnce.Do(func() {
		h.mu.Lock()
		h.stopLocked()
		h.mu.Unlock()
		h.Reject()
		h.Disconnect()
		h.wg.Wait()
	})
}

func (h *Host) acceptLoop(ctx context.Context, ln *session.Host) {
	defer h.wg.Done()
	for {
		p, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if !errors.Is(err, session.ErrBadPassword) {
				log.Printf("kkdesk host: accept: %v", err)
			}
			continue
		}
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.handle(p)
		}()
	}
}

// handle runs one incoming peer: busy check, confirmation, session.
func (h *Host) handle(p *session.Peer) {
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		p.CloseWithReason("对方正在被其他设备控制")
		return
	}
	h.busy = true
	h.peer, h.since = p, time.Now()
	h.notice = ""
	var ch chan bool
	if !h.cfg.AutoAccept {
		ch = make(chan bool, 1)
		h.decide = ch
		h.phase = HostPending
	}
	h.mu.Unlock()
	h.notify()

	defer func() {
		p.Close()
		h.mu.Lock()
		h.busy, h.peer, h.decide = false, nil, nil
		if h.ln != nil {
			h.phase = HostListening
		} else {
			h.phase = HostOff
		}
		h.mu.Unlock()
		h.notify()
	}()

	if ch != nil {
		t := time.NewTimer(ConfirmTimeout)
		defer t.Stop()
		ok := false
		select {
		case ok = <-ch:
		case <-t.C:
		case <-p.Done():
			return
		}
		if !ok {
			p.CloseWithReason("对方拒绝了连接请求")
			return
		}
	}
	if help := CaptureHelp(); help != "" {
		h.setErr(help)
		log.Printf("kkdesk host: %s", help)
		p.CloseWithReason("被控端没有屏幕录制权限")
		return
	}
	h.mu.Lock()
	h.phase, h.since = HostActive, time.Now()
	h.mu.Unlock()
	h.notify()
	if err := h.stream(p); err != nil {
		h.setErr(err.Error())
		log.Printf("kkdesk host: %v", err)
		p.CloseWithReason("被控端出错：" + err.Error())
		return
	}
	h.mu.Lock()
	h.notice = "上次连接已结束：" + p.RemoteAddr()
	h.mu.Unlock()
}

func (h *Host) setErr(s string) {
	h.mu.Lock()
	h.errMsg = s
	h.mu.Unlock()
	h.notify()
}

// stream runs capture → encode → peer until the peer is done.
func (h *Host) stream(p *session.Peer) error {
	rec := stats.NewRecorder(h.cfg.Log, "host", hostCPU())
	rec.Start()
	defer rec.Close()

	bps := AutoBitrate
	if h.cfg.BitrateMbps > 0 {
		bps = h.cfg.BitrateMbps * 1_000_000
	}
	vs := &videoSender{peer: p, rec: rec, fps: h.cfg.FPS, bps: bps}
	vs.forceKey.Store(true)
	p.OnKeyframeRequest(func() { vs.forceKey.Store(true) })

	h.wireInput(p)

	opts := capture.Options{FPS: h.cfg.FPS, MaxWidth: h.cfg.MaxWidth, ExcludeApp: captureExcludeApp(p.RemoteAddr())}
	log.Printf("kkdesk host: capture %d fps, max width %d, exclude %q", opts.FPS, opts.MaxWidth, opts.ExcludeApp)
	capt, err := capture.Start(opts, vs.frame)
	if err != nil {
		return fmt.Errorf("启动屏幕采集失败：%w", err)
	}
	vs.mu.Lock()
	vs.capt = capt
	vs.mu.Unlock()
	p.OnQuality(vs.setQuality)
	tk := time.NewTicker(StatsSummaryInterval)
loop:
	for {
		select {
		case <-p.Done():
			break loop
		case <-tk.C:
			log.Printf("kkdesk host: %s", statsSummary(rec.Last(), capt.Stats()))
		}
	}
	tk.Stop()
	st := capt.Stats()
	capt.Close() // no frame callbacks after this
	log.Printf("kkdesk host: capture frames complete=%d idle=%d other=%d display-after-callback=%d resent=%d reopened=%d",
		st.Complete, st.Idle, st.Other, st.Late, st.Resent, st.Reopened)
	vs.mu.Lock()
	if vs.enc != nil {
		vs.enc.Close()
		vs.enc = nil
	}
	vs.mu.Unlock()
	return nil
}

// injectLimitsOnce logs InjectLimits once per process.
var injectLimitsOnce sync.Once

// wireInput injects the controller's input when the platform permits it
// (macOS: Accessibility).
func (h *Host) wireInput(p *session.Peer) {
	var inj *inject.Injector
	if help := InjectHelp(); help == "" {
		if in, err := inject.New(); err == nil {
			inj = in
			b := in.Bounds()
			log.Printf("kkdesk host: inject: primary display %.0fx%.0f at (%.0f,%.0f)", b.W, b.H, b.X, b.Y)
		} else {
			log.Printf("kkdesk host: inject: %v", err)
		}
		if lim := InjectLimits(); lim != "" {
			injectLimitsOnce.Do(func() { log.Printf("kkdesk host: %s", lim) })
		}
	} else {
		log.Printf("kkdesk host: %s 远程输入将被忽略。", help)
	}
	var n atomic.Int64
	var errLogged atomic.Bool
	p.OnInput(func(e input.Event) {
		if inj == nil {
			return
		}
		if err := inj.Apply(e); err != nil {
			if !errLogged.Swap(true) {
				log.Printf("kkdesk host: inject: %v", err)
			}
			return
		}
		if n.Add(1) == 1 {
			log.Printf("kkdesk host: injected first remote input event (kind %d)", e.Kind)
		}
	})
}

// videoSender encodes captured frames and sends them to the peer.
type videoSender struct {
	peer     *session.Peer
	rec      *stats.Recorder
	forceKey atomic.Bool
	seq      atomic.Uint64

	mu       sync.Mutex
	capt     capture.Session
	enc      codec.Encoder
	encW     int
	encH     int
	fps, bps int
	failed   bool

	// display maps CaptureAt (UnixNano) → SCK display time of frames in
	// the encoder, for the capture_display / display_encoded samples.
	dispMu  sync.Mutex
	display map[int64]time.Time
}

// frame runs on the capture queue.
func (v *videoSender) frame(f capture.Frame) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed {
		return
	}
	if v.enc == nil || v.encW != f.Width || v.encH != f.Height {
		if v.enc != nil {
			v.enc.Close()
		}
		enc, err := codec.NewEncoder(f.Width, f.Height, v.fps, v.bps, v.encoded)
		if err != nil {
			log.Printf("kkdesk host: encoder: %v", err)
			v.failed = true
			v.peer.CloseWithReason("被控端无法创建编码器")
			return
		}
		v.enc, v.encW, v.encH = enc, f.Width, f.Height
		log.Printf("kkdesk host: encoder %dx%d %d fps %d bps: %s", f.Width, f.Height, v.fps, v.bps, EncoderInfo(enc))
		v.forceKey.Store(true)
	}
	if !f.DisplayAt.IsZero() {
		v.rec.Sample(stats.SegCaptureDisplay, f.DisplayAt.Sub(f.CaptureAt))
		v.dispMu.Lock()
		if v.display == nil || len(v.display) > 64 { // drop entries of dropped frames
			v.display = map[int64]time.Time{}
		}
		v.display[f.CaptureAt.UnixNano()] = f.DisplayAt
		v.dispMu.Unlock()
	}
	if err := v.enc.Encode(codec.Frame{Pixels: f.Pixels, CaptureAt: f.CaptureAt}, v.forceKey.Swap(false)); err != nil {
		log.Printf("kkdesk host: encode: %v", err)
	}
}

// encoded runs on an encoder thread.
func (v *videoSender) encoded(nal []byte, captureAt time.Time, keyframe bool) {
	encodedAt := time.Now()
	v.dispMu.Lock()
	disp, ok := v.display[captureAt.UnixNano()]
	delete(v.display, captureAt.UnixNano())
	v.dispMu.Unlock()
	if ok {
		v.rec.Sample(stats.SegDisplayEncoded, encodedAt.Sub(disp))
	}
	if err := v.peer.WriteVideo(nal, captureAt, keyframe); err != nil {
		return
	}
	sentAt := time.Now()
	v.rec.AddBytes(len(nal))
	v.rec.Frame(stats.FrameTimes{Seq: v.seq.Add(1), Capture: captureAt, Encoded: encodedAt, Sent: sentAt})
}

// setQuality applies the controller's quality choice.
func (v *videoSender) setQuality(q session.Quality) {
	bps := q.BitrateBps
	if bps <= 0 {
		bps = AutoBitrate
	}
	v.mu.Lock()
	v.bps = bps
	if v.enc != nil {
		v.enc.SetBitrate(bps)
	}
	var capt capture.Session
	if q.FPS > 0 && q.FPS != v.fps {
		v.fps = q.FPS
		if v.enc != nil {
			v.enc.SetFPS(q.FPS)
		}
		capt = v.capt
	}
	fps := v.fps
	v.mu.Unlock()
	if capt != nil {
		capt.SetFPS(fps) // outside mu: it waits for ScreenCaptureKit
	}
	log.Printf("kkdesk host: quality %d bps, %d fps", bps, fps)
}
