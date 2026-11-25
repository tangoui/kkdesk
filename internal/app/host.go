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
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/codec"
	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/files"
	"github.com/tangoui/kkdesk/internal/inject"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/rendezvous"
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
	Addr string // listen address; default ":21118"
	// Password is the temporary password; empty generates a 6-digit one.
	// PasswordSince is when it was made (zero: now), for a timed Rotate.
	Password      string
	PasswordSince time.Time
	// Fixed is the fixed password (设置 › 安全), accepted next to the
	// temporary one; "" for none.
	Fixed string
	// Rotate is the temporary password policy (config.Rotate*); "" is
	// config.RotateNever.
	Rotate string
	// OnPassword is called with every new temporary password (to save
	// it). It runs with the host locked: it must not call the host.
	OnPassword func(config.TempPassword)
	// OnPortMoved is called with the port the host moved to because the
	// system refused the configured one (to save it, so the next start
	// listens there directly and the address stays the same).
	OnPortMoved func(port int)
	FPS         int // capture frame rate
	MaxWidth    int // capture width limit; 0 = native
	BitrateMbps int // 0 = auto
	Log         io.Writer
	AutoAccept  bool
	Remote      rendezvous.Config
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
	Enabled  bool // listening wanted (SetEnabled(true)), even while a busy port is retried
	Phase    HostPhase
	Addrs    []string // local IPv4 addresses
	Port     int
	Password string // temporary password
	// PasswordSince is when the temporary password was made.
	PasswordSince time.Time
	Rotate        string    // temporary password policy (config.Rotate*)
	HasFixed      bool      // a fixed password is set
	AutoAccept    bool      // connections are accepted without asking
	Peer          string    // remote address while pending or active
	Since         time.Time // active: session start; pending: request time
	Deadline      time.Time // pending: automatic rejection
	Err           string    // last error or permission guidance
	Notice        string    // informational message (e.g. last session ended)
	// FilesOnly: the controller connected only to transfer files (no
	// screen is shared).
	FilesOnly bool
	// FileAccess: the controller may browse and transfer files (the
	// 文件传输 permission).
	FileAccess bool
	RemoteCode string
	RemoteErr  string
	RemotePeer bool
}

// Host accepts controllers and streams this screen to one at a time. Its
// methods are safe for concurrent use.
type Host struct {
	cfg    HostConfig
	notify func()

	mu         sync.Mutex
	password   string
	pwSince    time.Time
	pwTimer    *time.Timer // timed rotation
	ln         *session.Host
	cancel     context.CancelFunc
	phase      HostPhase
	peer       *session.Peer
	since      time.Time
	decide     chan bool
	errMsg     string
	notice     string
	busy       bool
	wg         sync.WaitGroup
	addrs      []string
	stopOnce   sync.Once
	fsrv       *files.Server // file server of the current session
	wantOn     bool          // SetEnabled(true) and not turned off since
	retry      *time.Timer   // next listen attempt while the port is busy
	remote     remoteHostState
	closed     bool
	remotePeer bool

	fileAccess atomic.Bool // the 文件传输 permission; on by default
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
	if cfg.Rotate == "" {
		cfg.Rotate = config.RotateNever
	}
	h := &Host{cfg: cfg, notify: notify, addrs: LocalIPv4()}
	if cfg.Remote.URL != "" {
		h.remote.code = rendezvous.NewCode()
	}
	h.fileAccess.Store(true)
	h.mu.Lock()
	if cfg.Password == "" {
		h.newPasswordLocked()
	} else {
		h.password, h.pwSince = cfg.Password, cfg.PasswordSince
		if h.pwSince.IsZero() {
			h.pwSince = time.Now()
		}
		h.scheduleLocked() // a saved password may be due already
	}
	h.mu.Unlock()
	return h
}

// newPasswordLocked makes a new temporary password: the listener accepts
// it (and no longer the old one) from the next connection on.
func (h *Host) newPasswordLocked() {
	h.password, h.pwSince = NewPassword(), time.Now()
	if h.ln != nil {
		h.ln.SetPasswords(h.password, h.cfg.Fixed)
	}
	if h.cfg.OnPassword != nil {
		h.cfg.OnPassword(config.TempPassword{Password: h.password, Since: h.pwSince})
	}
	h.scheduleLocked()
}

// scheduleLocked arms the timer of a timed rotation policy, or rotates at
// once when the password is due.
func (h *Host) scheduleLocked() {
	if h.pwTimer != nil {
		h.pwTimer.Stop()
		h.pwTimer = nil
	}
	every := config.RotateEvery(h.cfg.Rotate)
	if every <= 0 {
		return
	}
	d := time.Until(h.pwSince.Add(every))
	if d <= 0 {
		h.newPasswordLocked()
		return
	}
	h.pwTimer = time.AfterFunc(d, func() {
		h.mu.Lock()
		due := config.RotateEvery(h.cfg.Rotate) > 0 && !time.Now().Before(h.pwSince.Add(config.RotateEvery(h.cfg.Rotate)))
		if due {
			h.newPasswordLocked()
		}
		h.mu.Unlock()
		if due {
			log.Printf("kkdesk host: temporary password rotated (%s)", h.cfg.Rotate)
			h.notify()
		}
	})
}

// SetRotate changes the temporary password policy (config.Rotate*). A
// timed policy whose period already passed rotates at once.
func (h *Host) SetRotate(policy string) {
	h.mu.Lock()
	h.cfg.Rotate = policy
	h.scheduleLocked()
	h.mu.Unlock()
	h.notify()
}

// SetFixedPassword sets the fixed password ("" clears it). The old one
// stops working at the next connection; running sessions go on.
func (h *Host) SetFixedPassword(pw string) {
	h.mu.Lock()
	h.cfg.Fixed = pw
	if h.ln != nil {
		h.ln.SetPasswords(h.password, pw)
	}
	h.mu.Unlock()
	h.notify()
}

// SetAutoAccept turns the confirmation of incoming connections off (true)
// or on; it applies to the next connection.
func (h *Host) SetAutoAccept(on bool) {
	h.mu.Lock()
	h.cfg.AutoAccept = on
	h.mu.Unlock()
	h.notify()
}

// SetQuality sets the frame rate and bitrate (Mbps, 0 = auto) the next
// session starts with; the controller may still change them.
func (h *Host) SetQuality(fps, bitrateMbps int) {
	h.mu.Lock()
	if fps > 0 {
		h.cfg.FPS = fps
	}
	h.cfg.BitrateMbps = bitrateMbps
	h.mu.Unlock()
}

// SetAddr changes the listen address. If the host listens (or retries a
// busy port) it restarts the listener on the new address; a running
// session is not affected and the new address only serves later
// connections.
func (h *Host) SetAddr(addr string) error {
	h.mu.Lock()
	defer h.notify()
	defer h.mu.Unlock()
	if addr == h.cfg.Addr {
		return nil
	}
	h.cfg.Addr = addr
	if !h.wantOn {
		return nil
	}
	h.stopLocked()
	return h.startLocked()
}

// FileAccess reports whether the controller may browse and transfer files.
func (h *Host) FileAccess() bool { return h.fileAccess.Load() }

// SetFileAccess turns the 文件传输 permission on or off. Turning it off
// aborts running transfers at once; the controller sees ErrDenied.
func (h *Host) SetFileAccess(on bool) {
	if h.fileAccess.Swap(on) == on {
		return
	}
	h.mu.Lock()
	srv := h.fsrv
	h.mu.Unlock()
	if on {
		log.Printf("kkdesk host: file access on")
	} else {
		log.Printf("kkdesk host: file access off")
	}
	if srv != nil {
		srv.CheckAllowed()
	}
	h.notify()
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
// excluding loopback, the most reachable first (see rankAddrs).
func LocalIPv4() []string { return localIPv4() }

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
	s := HostState{Phase: h.phase, Addrs: h.addrs, Port: Port, Password: h.password, Err: h.errMsg, Notice: h.notice,
		FileAccess: h.fileAccess.Load(), Enabled: h.wantOn, PasswordSince: h.pwSince, Rotate: h.cfg.Rotate,
		HasFixed: h.cfg.Fixed != "", AutoAccept: h.cfg.AutoAccept}
	if h.remote.online {
		s.RemoteCode = h.remote.code
	}
	s.RemoteErr = h.remote.err
	if tcp, err := net.ResolveTCPAddr("tcp", h.cfg.Addr); err == nil && tcp.Port != 0 {
		s.Port = tcp.Port
	}
	if h.ln != nil {
		if tcp, ok := h.ln.Addr().(*net.TCPAddr); ok && tcp.Port != 0 {
			s.Port = tcp.Port // the port actually in use (it can differ, see startLocked)
		}
	}
	if h.peer != nil {
		s.Peer = h.peer.RemoteAddr()
		s.RemotePeer = h.remotePeer
		s.Since = h.since
		s.FilesOnly = h.peer.Mode() == session.ModeFiles
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
	if h.closed || (on == h.wantOn && (!on || h.ln != nil || h.retry != nil)) {
		return nil
	}
	h.wantOn = on
	if !on {
		h.stopLocked()
		h.stopRemoteLocked()
		if !h.busy {
			h.phase = HostOff
		}
		return nil
	}
	h.startRemoteLocked()
	return h.startLocked()
}

// listenRetryEvery is how often a busy port is tried again (a variable for
// tests).
var listenRetryEvery = time.Second

// listen opens the signaling listener (a variable for tests).
var listen = session.Listen

// altPorts is how many ports after the configured one are tried when the
// system refuses it.
const altPorts = 20

func (h *Host) startLocked() error {
	ln, err := listen(h.cfg.Addr, h.password, h.cfg.Fixed)
	if err != nil && addrDenied(err) {
		// Windows reserves port ranges for Hyper-V, WSL and Docker
		// (netsh interface ipv4 show excludedportrange protocol=tcp);
		// binding inside one fails with WSAEACCES. Try the next ports,
		// then let the system pick one: a reservation often spans 100
		// ports or more, and the system never hands out a reserved port.
		host, port := splitPort(h.cfg.Addr)
		cands := []int{}
		for i := 1; i <= altPorts && port > 0 && port+i <= 65535; i++ {
			cands = append(cands, port+i)
		}
		cands = append(cands, 0)
		for _, p := range cands {
			l2, err2 := listen(net.JoinHostPort(host, strconv.Itoa(p)), h.password, h.cfg.Fixed)
			if err2 == nil {
				ln, err = l2, nil
				break
			}
			if !addrDenied(err2) && !addrInUse(err2) {
				break
			}
		}
		if err != nil {
			h.errMsg = fmt.Sprintf("端口 %d 被系统保留，也没能找到其他可用端口，请在「设置 › 网络」里换一个端口", port)
			return err
		}
		got := ln.Addr().(*net.TCPAddr).Port
		h.cfg.Addr = net.JoinHostPort(host, strconv.Itoa(got)) // kept when the listener restarts
		h.notice = fmt.Sprintf("端口 %d 被系统保留（常见于 Hyper-V、WSL、Docker），已改用 %d", port, got)
		if h.cfg.OnPortMoved != nil {
			h.cfg.OnPortMoved(got)
			h.notice += "，并设为默认端口"
		}
	}
	if err != nil {
		if addrInUse(err) {
			// The port is often held by a process that is exiting (a hot
			// reload, a quick restart): keep trying until it frees up or
			// the host is turned off.
			h.errMsg = fmt.Sprintf("端口 %s 正被占用，正在重试…", portOf(h.cfg.Addr))
			h.retry = time.AfterFunc(listenRetryEvery, h.retryListen)
			return nil
		}
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

// retryListen is one more listen attempt after the port was busy.
func (h *Host) retryListen() {
	h.mu.Lock()
	h.retry = nil
	if !h.wantOn || h.ln != nil {
		h.mu.Unlock()
		return
	}
	_ = h.startLocked() // reschedules itself while the port stays busy
	h.mu.Unlock()
	h.notify()
}

// addrInUse reports a listen error caused by a port that is already taken.
func addrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == 10048 // WSAEADDRINUSE
}

// addrDenied reports a listen error caused by the system refusing the port
// (Windows WSAEACCES 10013 inside a reserved range; EACCES elsewhere).
func addrDenied(err error) bool {
	if errors.Is(err, syscall.EACCES) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == 10013 // WSAEACCES
}

// splitPort splits a listen address such as ":21118"; port is 0 when it
// cannot be parsed.
func splitPort(addr string) (host string, port int) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

// portOf returns the port of a listen address such as ":21118".
func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}

func (h *Host) stopLocked() {
	if h.retry != nil {
		h.retry.Stop()
		h.retry = nil
		h.errMsg = ""
	}
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

// RefreshPassword generates a new temporary password; the old one stops
// working at once. An active session is not affected.
func (h *Host) RefreshPassword() {
	h.mu.Lock()
	h.newPasswordLocked()
	h.mu.Unlock()
	h.notify()
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
		h.closed = true
		h.stopLocked()
		h.stopRemoteLocked()
		if h.pwTimer != nil {
			h.pwTimer.Stop()
			h.pwTimer = nil
		}
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
			if !errors.Is(err, session.ErrBadPassword) && !errors.Is(err, session.ErrProbe) {
				log.Printf("kkdesk host: accept: %v", err)
			}
			continue
		}
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.handle(p, nil)
		}()
	}
}

// handle runs one incoming peer: busy check, confirmation, session.
func (h *Host) handle(p *session.Peer, remoteCtx context.Context) {
	h.mu.Lock()
	if h.closed || (remoteCtx != nil && (remoteCtx.Err() != nil || !h.wantOn)) {
		h.mu.Unlock()
		p.CloseWithReason("被控端已关闭远程连接")
		return
	}
	if h.busy {
		h.mu.Unlock()
		p.CloseWithReason("对方正在被其他设备控制")
		return
	}
	h.busy = true
	h.peer, h.since = p, time.Now()
	h.remotePeer = remoteCtx != nil
	h.notice = ""
	var ch chan bool
	if remoteCtx != nil || !h.cfg.AutoAccept {
		ch = make(chan bool, 1)
		h.decide = ch
		h.phase = HostPending
	}
	h.mu.Unlock()
	h.notify()

	accepted := false // the session got past the confirmation
	defer func() {
		p.Close()
		h.mu.Lock()
		h.busy, h.peer, h.decide, h.fsrv = false, nil, nil, nil
		h.remotePeer = false
		if accepted && h.cfg.Rotate == config.RotateAfterSession {
			h.newPasswordLocked() // 每次连接后更换
			log.Printf("kkdesk host: temporary password rotated after the session")
		}
		if h.ln != nil || h.remote.online {
			h.phase = HostListening
		} else {
			h.phase = HostOff
		}
		h.mu.Unlock()
		h.notify()
	}()

	if ch != nil {
		var disabled <-chan struct{}
		if remoteCtx != nil {
			disabled = remoteCtx.Done()
		}
		t := time.NewTimer(ConfirmTimeout)
		defer t.Stop()
		ok := false
		select {
		case ok = <-ch:
		case <-t.C:
		case <-p.Done():
			return
		case <-disabled:
		}
		if !ok || (remoteCtx != nil && remoteCtx.Err() != nil) {
			p.CloseWithReason("对方拒绝了连接请求")
			return
		}
	}
	accepted = true
	h.serveFiles(p)
	if p.Mode() == session.ModeFiles {
		h.mu.Lock()
		h.phase, h.since = HostActive, time.Now()
		h.mu.Unlock()
		h.notify()
		log.Printf("kkdesk host: %s connected to transfer files", p.RemoteAddr())
		<-p.Done()
		h.mu.Lock()
		h.notice = "上次连接已结束：" + p.RemoteAddr()
		h.mu.Unlock()
		return
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

// serveFiles starts the file server of an accepted session (both modes);
// it stops when the session closes.
func (h *Host) serveFiles(p *session.Peer) {
	ch := p.FileChannel()
	if ch == nil {
		return
	}
	srv := files.NewServer(ch, h.fileAccess.Load)
	srv.SetLogf(func(format string, args ...any) { log.Printf("kkdesk host: "+format, args...) })
	h.mu.Lock()
	h.fsrv = srv
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

	h.mu.Lock()
	fps, mbps, maxW := h.cfg.FPS, h.cfg.BitrateMbps, h.cfg.MaxWidth
	h.mu.Unlock()
	bps := AutoBitrate
	if mbps > 0 {
		bps = mbps * 1_000_000
	}
	vs := &videoSender{peer: p, rec: rec, fps: fps, bps: bps}
	vs.forceKey.Store(true)
	p.OnKeyframeRequest(func() { vs.forceKey.Store(true) })

	h.wireInput(p)

	opts := capture.Options{FPS: fps, MaxWidth: maxW, ExcludeApp: captureExcludeApp(p.RemoteAddr())}
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
