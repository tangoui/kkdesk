package lockscreen

import (
	"errors"
	"testing"
)

func TestMacLockScreenRequiresTrustedPeerAndLockedConsole(t *testing.T) {
	self := macCodeIdentity{team: "TEAM123456", identifier: "com.kkdesk.desktop"}
	user := macPeer{uid: 501, code: self}
	root := macPeer{uid: 0, code: self}
	if !macPeerAllowed(self, user, 501) || !macPeerAllowed(self, root, 0) {
		t.Fatal("valid host or daemon was refused")
	}
	for _, other := range []macPeer{
		{uid: 502, code: self},
		{uid: 501, code: macCodeIdentity{}},
		{uid: 501, code: macCodeIdentity{team: "OTHERTEAM1", identifier: self.identifier}},
		{uid: 501, code: macCodeIdentity{team: self.team, identifier: "com.other.app"}},
	} {
		if macPeerAllowed(self, other, 501) {
			t.Errorf("accepted unexpected peer %+v", other)
		}
	}
	if macPeerAllowed(macCodeIdentity{}, macPeer{uid: 501}, 501) {
		t.Fatal("an unsigned build accepted an unsigned peer")
	}
	if !macConsoleAllows(501, 501, true) ||
		macConsoleAllows(501, 502, true) || macConsoleAllows(501, 501, false) || macConsoleAllows(0, 0, true) {
		t.Fatal("accepted an unlocked, changed, or signed-out console")
	}
}

func TestMacLockScreenFrameSlots(t *testing.T) {
	offset, size, ok := macFrameBounds(3456, 2234, 1)
	if !ok || offset != macFrameSlotBytes || size != 3456*2234*3/2 {
		t.Fatalf("invalid first frame: %d %d %t", offset, size, ok)
	}
	offset, _, ok = macFrameBounds(3456, 2234, 2)
	if !ok || offset != 0 {
		t.Fatal("frames did not alternate slots")
	}
	for _, frame := range [][3]uint64{{1, 200, 1}, {200, 201, 1}, {0, 200, 1}, {40960, 2160, 1}, {200, 200, 0}} {
		if _, _, ok := macFrameBounds(uint32(frame[0]), uint32(frame[1]), frame[2]); ok {
			t.Fatalf("accepted invalid frame %v", frame)
		}
	}
}

func TestMacLockScreenRegistrationRollbackAndApproval(t *testing.T) {
	states := map[bool]int{false: macNotRegistered, true: macNotRegistered}
	fail := errors.New("agent registration failed")
	status := func(agent bool) int { return states[agent] }
	change := func(agent, install bool) error {
		if install && agent {
			return fail
		}
		if install {
			states[agent] = macNeedsApproval
		} else {
			states[agent] = macNotRegistered
		}
		return nil
	}
	if err := changeMacJobs(true, status, change); !errors.Is(err, fail) || status(false) != macNotRegistered {
		t.Fatalf("failed registration was not rolled back: %v %v", err, states)
	}
	if got := macServiceState(macNeedsApproval, macEnabled); got != ComponentNeedsApproval {
		t.Fatalf("approval not reported: %v", got)
	}
	states[false], states[true] = macEnabled, macEnabled
	if err := changeMacJobs(false, status, change); err != nil || status(false) != macNotRegistered || status(true) != macNotRegistered {
		t.Fatalf("jobs were not removed: %v %v", err, states)
	}
}
