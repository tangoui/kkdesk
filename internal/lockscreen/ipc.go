package lockscreen

import (
	"encoding/hex"
	"strconv"
	"strings"
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
	return session != ^uint32(0) && session == active && locked && strings.EqualFold(desktop, "Winlogon")
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
