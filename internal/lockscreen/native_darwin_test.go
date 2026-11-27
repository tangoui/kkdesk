//go:build darwin && cgo

package lockscreen

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/tangoui/kkdesk/internal/capture"
)

func TestMacLockScreenUnsignedRequiresSignature(t *testing.T) {
	if macOwnCode().team != "" {
		t.Skip("test executable is developer signed")
	}
	info, err := Component()
	if err != nil || info.State != ComponentNeedsSignature {
		t.Fatalf("unsigned component state = %v, error = %v", info.State, err)
	}
	_, err = startMac(context.Background(), capture.Options{}, nil)
	if !errors.Is(err, ErrNeedsSignature) {
		t.Fatalf("unsigned component attempted to start: %v", err)
	}
}

func TestMacLockScreenSharedFramesReadOnly(t *testing.T) {
	writable, reader, err := macSharedFrames()
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(writable)
	defer reader.Close()
	fd := int(reader.Fd())
	mode, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || mode&unix.O_ACCMODE != unix.O_RDONLY {
		t.Fatalf("reader descriptor mode = %x, error = %v", mode, err)
	}
	view, err := unix.Mmap(fd, 0, 2*macFrameSlotBytes, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(view)
	writable[macFrameSlotBytes] = 125
	if view[macFrameSlotBytes] != 125 {
		t.Fatal("published slot is not shared")
	}
}

func TestMacLockScreenPassesSharedFrameDescriptor(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	files := [2]*os.File{os.NewFile(uintptr(pair[0]), "lock screen socket"), os.NewFile(uintptr(pair[1]), "lock screen socket")}
	for _, f := range files {
		defer f.Close()
	}
	convert := func(f *os.File) *net.UnixConn {
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c.(*net.UnixConn)
	}
	client, server := convert(files[0]), convert(files[1])
	mapping, reader, err := macSharedFrames()
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(mapping)
	defer reader.Close()
	if err := macSendFD(server, int(reader.Fd())); err != nil {
		t.Fatal(err)
	}
	fd, err := macReceiveFD(client)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	var sent, received unix.Stat_t
	if unix.Fstat(int(reader.Fd()), &sent) != nil || unix.Fstat(fd, &received) != nil ||
		sent.Ino != received.Ino || received.Size != 2*macFrameSlotBytes {
		t.Fatal("received the wrong shared memory descriptor")
	}
}
