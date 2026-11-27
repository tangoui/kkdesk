package lockscreen

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	replyOK byte = iota + 1
	replyIdle
	replyUnavailable
)

const (
	ipcFrame byte = iota + 1
	ipcInput
	ipcUnlock
	ipcSAS
)

const (
	maxFrameWidth  = 3840
	maxFrameHeight = 2160
	frameSlotBytes = maxFrameWidth * maxFrameHeight * 4
	maxSecretBytes = 1024
)

type localIdentity struct {
	pid, session uint32
	sid, image   string
}

func (i localIdentity) matches(console localIdentity) bool {
	return i.pid != 0 && i.session == console.session && i.sid != "" &&
		i.sid == console.sid && i.image != "" && strings.EqualFold(i.image, console.image)
}

func allowLockedDesktop(session, active uint32, locked bool, desktop string) bool {
	return lockedDesktopProblem(session, active, locked, desktop) == ""
}

// lockedDesktopProblem says why the helper may not serve a session, or ""
// when it is the active console, locked, on the Winlogon desktop.
func lockedDesktopProblem(session, active uint32, locked bool, desktop string) string {
	switch {
	case session == ^uint32(0):
		return "no session"
	case session != active:
		return fmt.Sprintf("session %d is not the active console (%d)", session, active)
	case !locked:
		return fmt.Sprintf("session %d is not locked", session)
	case !strings.EqualFold(desktop, "Winlogon"):
		return fmt.Sprintf("input desktop is %q, not Winlogon", desktop)
	}
	return ""
}

func frameBounds(width, height uint32, sequence uint64) (offset, size int, ok bool) {
	if sequence == 0 || width == 0 || height == 0 || width > maxFrameWidth || height > maxFrameHeight {
		return 0, 0, false
	}
	return int(sequence%2) * frameSlotBytes, int(width*height) * 4, true
}

func helperLaunchArgs(args []string) (uint32, string, bool) {
	if len(args) != 3 || args[0] != "-lockscreen-helper" || len(args[2]) != 32 {
		return 0, "", false
	}
	pid, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil || pid == 0 {
		return 0, "", false
	}
	var nonce [16]byte
	if _, err := hex.Decode(nonce[:], []byte(args[2])); err != nil {
		return 0, "", false
	}
	return uint32(pid), args[2], true
}

func receiveCommand(r io.Reader, data []byte) (byte, []byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.LittleEndian.Uint16(header[1:]))
	if n > len(data) {
		return 0, nil, errors.New("lock screen command exceeds message limit")
	}
	data = data[:n]
	if _, err := io.ReadFull(r, data); err != nil {
		clear(data)
		return 0, nil, err
	}
	return header[0], data, nil
}

func sendCommand(w io.Writer, operation byte, data []byte) error {
	if len(data) > maxSecretBytes {
		return errors.New("lock screen command exceeds message limit")
	}
	var frame [3 + maxSecretBytes]byte
	frame[0] = operation
	binary.LittleEndian.PutUint16(frame[1:], uint16(len(data)))
	n := copy(frame[3:], data)
	defer clear(frame[3 : 3+n])
	written, err := w.Write(frame[:3+n])
	if err == nil && written != 3+n {
		return io.ErrShortWrite
	}
	return err
}
