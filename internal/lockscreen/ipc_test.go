package lockscreen

import (
	"errors"
	"strings"
	"testing"
)

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

// A locked Windows 11 console shows the lock screen curtain (LockApp) on
// the Default desktop and the sign-in page on Winlogon: both are served.
func TestIPCSecureDesktopRequiresLockedConsole(t *testing.T) {
	if !allowLockedDesktop(3, 3, true, "Winlogon") || !allowLockedDesktop(3, 3, true, "Default") {
		t.Fatal("valid locked console was rejected")
	}
	if allowLockedDesktop(3, 3, false, "Winlogon") ||
		allowLockedDesktop(3, 3, false, "Default") ||
		allowLockedDesktop(3, 4, true, "Winlogon") ||
		allowLockedDesktop(3, 3, true, "Screen-saver") {
		t.Fatal("allowed an unlocked, inactive, or unexpected desktop")
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
		{3, 3, true, "Default", ""},
		{3, 3, true, "Screen-saver", `input desktop is "Screen-saver", not Winlogon or Default`},
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

// Typing on the lock screen curtain moves the console to the sign-in page;
// the next keys fail on the old desktop until the helper follows it.
func TestApplyFollowingRetriesOnTheNewDesktop(t *testing.T) {
	onNew := false
	tries, enters := 0, 0
	apply := func() error {
		tries++
		if !onNew {
			return errors.New("SendInput sent 0 of 1 events")
		}
		return nil
	}
	enter := func() error { enters++; onNew = true; return nil }
	if err := applyFollowing(apply, enter); err != nil || tries != 2 || enters != 1 {
		t.Fatalf("applyFollowing = %v after %d tries and %d desktop switches, want success after 2 and 1", err, tries, enters)
	}
	if err := applyFollowing(apply, enter); err != nil || tries != 3 || enters != 1 {
		t.Fatalf("a working desktop was switched again: %v, %d tries, %d switches", err, tries, enters)
	}
	stuck := errors.New("access denied")
	if err := applyFollowing(func() error { return stuck }, func() error { return errors.New("no desktop") }); err == nil ||
		!strings.Contains(err.Error(), "access denied") || !strings.Contains(err.Error(), "no desktop") {
		t.Fatalf("a failed switch should report both errors, got %v", err)
	}
}
