package lockscreen

import "strings"

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
