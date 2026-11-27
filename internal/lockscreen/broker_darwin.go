//go:build darwin && cgo

package lockscreen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	macSocket = "/var/run/com.kkdesk.lockscreen.sock"
	macHost   = 1
	macAgent  = 2
	macFD     = 3
)

type macBroker struct {
	mu      sync.Mutex
	agent   *net.UnixConn
	agentID uint32
	active  bool
}

func runMacDaemon() error {
	if info, err := os.Lstat(macSocket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("lock screen socket path occupied")
		}
		if err := os.Remove(macSocket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: macSocket, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(macSocket, 0666); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	var broker macBroker
	pending := make(chan struct{}, 16)
	for {
		c, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case pending <- struct{}{}:
			go func() {
				defer func() { <-pending }()
				broker.accept(c)
			}()
		default:
			_ = c.Close()
		}
	}
}

func (b *macBroker) accept(c *net.UnixConn) {
	uid := macConsoleUser()
	if uid == 0 || !macAuthorize(c, uid) {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var op [1]byte
	if _, err := io.ReadFull(c, op[:]); err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	if op[0] == macAgent {
		b.mu.Lock()
		if b.agent != nil {
			_ = b.agent.Close()
		}
		b.agent, b.agentID = c, uid
		b.mu.Unlock()
		return
	}
	defer c.Close()
	if op[0] != macHost || !macLocked(uid) {
		return
	}
	b.mu.Lock()
	if b.active || b.agent == nil || b.agentID != uid {
		b.mu.Unlock()
		return
	}
	agent := b.agent
	b.agent, b.active = nil, true
	b.mu.Unlock()
	defer func() {
		_ = agent.Close()
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
	}()

	_ = agent.SetWriteDeadline(time.Now().Add(time.Second))
	file, err := c.File()
	if err != nil {
		return
	}
	err = macSendFD(agent, int(file.Fd()))
	_ = file.Close()
	if err != nil {
		return
	}
	defer macAbort(c)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = agent.Read(op[:])
	}()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			if !macLocked(uid) {
				return
			}
		}
	}
}

func macDial(ctx context.Context, role byte) (*net.UnixConn, error) {
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "unix", macSocket)
	if err != nil {
		return nil, fmt.Errorf("%w: dial macOS lock screen daemon: %w", ErrService, err)
	}
	c := conn.(*net.UnixConn)
	if !macAuthorize(c, 0) {
		_ = c.Close()
		return nil, fmt.Errorf("%w: macOS lock screen daemon identity could not be verified", ErrNeedsSignature)
	}
	_ = c.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := c.Write([]byte{role}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("%w: send macOS lock screen handshake: %w", ErrService, err)
	}
	return c, nil
}

func macSendFD(c *net.UnixConn, fd int) error {
	_, _, err := c.WriteMsgUnix([]byte{macFD}, unix.UnixRights(fd), nil)
	return err
}

func macReceiveFD(c *net.UnixConn) (int, error) {
	var code [1]byte
	control := make([]byte, unix.CmsgSpace(4))
	n, ctl, flags, _, err := c.ReadMsgUnix(code[:], control)
	if err != nil {
		return -1, err
	}
	msgs, err := unix.ParseSocketControlMessage(control[:ctl])
	if err != nil {
		return -1, err
	}
	var fds []int
	for _, msg := range msgs {
		if msg.Header.Level == unix.SOL_SOCKET && msg.Header.Type == unix.SCM_RIGHTS {
			rights, _ := unix.ParseUnixRights(&msg)
			fds = append(fds, rights...)
		}
	}
	if n == 1 && code[0] == macFD && flags&unix.MSG_CTRUNC == 0 && len(fds) == 1 {
		unix.CloseOnExec(fds[0])
		return fds[0], nil
	}
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
	return -1, ErrUnavailable
}

func macAbort(c *net.UnixConn) {
	if raw, err := c.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) { _ = unix.Shutdown(int(fd), unix.SHUT_RDWR) })
	}
}

func runMacAgent() error {
	uid := uint32(os.Geteuid())
	for {
		if macConsoleUser() == uid {
			if err := macAgentSession(uid); err != nil && !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrService) &&
				!errors.Is(err, io.EOF) && !errors.Is(err, os.ErrDeadlineExceeded) {
				log.Printf("kkdesk lock screen agent: %v", err)
			}
		}
		time.Sleep(time.Second)
	}
}

func macAgentSession(uid uint32) error {
	broker, err := macDial(context.Background(), macAgent)
	if err != nil {
		return err
	}
	defer broker.Close()
	_ = broker.SetReadDeadline(time.Now().Add(time.Minute))
	fd, err := macReceiveFD(broker)
	if err != nil {
		return err
	}
	_ = broker.SetReadDeadline(time.Time{})
	f := os.NewFile(uintptr(fd), "lock screen session")
	c, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	conn := c.(*net.UnixConn)
	defer conn.Close()
	if !macLocked(uid) || !macAuthorize(conn, uid) {
		return ErrUnavailable
	}
	alive := make(chan struct{})
	go func() {
		defer close(alive)
		var end [1]byte
		_, _ = broker.Read(end[:])
		_ = conn.Close()
	}()
	defer func() {
		_ = broker.Close()
		<-alive
	}()
	valid := func() bool {
		select {
		case <-alive:
			return false
		default:
			return macLocked(uid)
		}
	}
	return serveMacSession(conn, valid)
}
