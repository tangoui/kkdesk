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
