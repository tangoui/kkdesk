package lockscreen

import "testing"

func TestIPCClientIdentity(t *testing.T) {
	want := localIdentity{
		pid: 0, sid: "S-1-5-21-1", session: 4, image: `C:\Program Files\kkdesk\kkdesk.exe`,
	}
	client := localIdentity{
		pid: 21, sid: want.sid, session: want.session, image: `c:\PROGRAM FILES\kkdesk\kkdesk.exe`,
	}
	if !client.matches(want) {
		t.Fatal("same console user, session, and protected image were rejected")
	}
	for _, other := range []localIdentity{
		{pid: 0, sid: client.sid, session: client.session, image: client.image},
		{pid: client.pid, sid: "S-1-5-21-2", session: client.session, image: client.image},
		{pid: client.pid, sid: client.sid, session: 8, image: client.image},
		{pid: client.pid, sid: client.sid, session: client.session, image: `C:\Users\user\kkdesk.exe`},
	} {
		if other.matches(want) {
			t.Errorf("accepted unexpected client %+v", other)
		}
	}
}

func TestIPCSecureDesktopRequiresLockedConsole(t *testing.T) {
	if !allowLockedDesktop(3, 3, true, "Winlogon") {
		t.Fatal("valid locked console was rejected")
	}
	if allowLockedDesktop(3, 3, false, "Winlogon") ||
		allowLockedDesktop(3, 4, true, "Winlogon") ||
		allowLockedDesktop(3, 3, true, "Default") {
		t.Fatal("allowed an unlocked, inactive, or non-Winlogon desktop")
	}
}

// The helper logs why it stops serving, so each refusal names its cause.
func TestLockedDesktopProblemNamesTheCause(t *testing.T) {
	for _, tc := range []struct {
		session, active uint32
		locked          bool
		desktop, want   string
	}{
		{3, 3, true, "Winlogon", ""},
		{3, 3, true, "winlogon", ""},
		{^uint32(0), 3, true, "Winlogon", "no session"},
		{3, 4, true, "Winlogon", "session 3 is not the active console (4)"},
		{3, 3, false, "Winlogon", "session 3 is not locked"},
		{3, 3, true, "Default", `input desktop is "Default", not Winlogon`},
	} {
		if got := lockedDesktopProblem(tc.session, tc.active, tc.locked, tc.desktop); got != tc.want {
			t.Errorf("lockedDesktopProblem(%d, %d, %v, %q) = %q, want %q", tc.session, tc.active, tc.locked, tc.desktop, got, tc.want)
		}
	}
}

func TestIPCFrameBoundsAndSlots(t *testing.T) {
	a, n, ok := frameBounds(1920, 1080, 1)
	if !ok || a != frameSlotBytes || n != 1920*1080*4 {
		t.Fatalf("bad first frame bounds %d %d %t", a, n, ok)
	}
	b, _, ok := frameBounds(1920, 1080, 2)
	if !ok || b != 0 || b == a {
		t.Fatal("sequential frames did not alternate slots")
	}
	if _, _, ok := frameBounds(0, 1080, 3); ok {
		t.Fatal("zero width was accepted")
	}
	if _, _, ok := frameBounds(4096, 4096, 3); ok {
		t.Fatal("oversized frame was accepted")
	}
	if _, _, ok := frameBounds(1920, 1080, 0); ok {
		t.Fatal("reserved frame sequence was accepted")
	}
}

func TestHelperLaunchRequiresServiceProcess(t *testing.T) {
	nonce := "A1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4"
	wantPID := uint32(321)
	owner, gotNonce, ok := helperLaunchArgs([]string{"-lockscreen-helper", "321", nonce})
	if !ok || owner != wantPID || gotNonce != nonce {
		t.Fatal("valid service ownership was rejected")
	}
	for _, args := range [][]string{
		{"-lockscreen-helper", nonce},
		{"-lockscreen-helper", "0", nonce},
		{"-lockscreen-helper", "321", "invalid-event"},
	} {
		if _, _, ok := helperLaunchArgs(args); ok {
			t.Fatalf("accepted invalid launch args %v", args)
		}
	}
}
