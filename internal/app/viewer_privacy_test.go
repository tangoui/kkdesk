package app

import (
	"testing"

	"github.com/tangoui/kkdesk/internal/session"
)

func TestViewerPrivacyUsesHostReports(t *testing.T) {
	v := NewViewer(ViewerConfig{Addr: "localhost", ViewOnly: true}, nil)
	v.handlePrivacy(session.PrivacyMessage{Kind: session.PrivacyState, Status: session.PrivacyWaiting})
	if s := v.State(); s.PrivacyScreen || s.PrivacyMessage != "等待对方同意…" {
		t.Fatalf("pending state = %+v", s)
	}
	v.handlePrivacy(session.PrivacyMessage{Kind: session.PrivacyState, Status: session.PrivacyDenied})
	if s := v.State(); s.PrivacyScreen || s.PrivacyMessage != "对方拒绝开启隐私屏" {
		t.Fatalf("denied state = %+v", s)
	}
	v.handlePrivacy(session.PrivacyMessage{Kind: session.PrivacyState, Status: session.PrivacyOn})
	v.handlePrivacy(session.PrivacyMessage{Kind: session.LockOnDisconnectState, Status: session.PrivacyOn})
	if s := v.State(); !s.PrivacyScreen || !s.LockAfterDisconnect {
		t.Fatalf("enabled state = %+v", s)
	}
	v.handlePrivacy(session.PrivacyMessage{Kind: session.PrivacyState, Status: session.PrivacyLocalExit})
	if s := v.State(); s.PrivacyScreen || !s.LockAfterDisconnect || s.PrivacyMessage != "对方关闭了隐私屏" {
		t.Fatalf("local exit state = %+v", s)
	}
}
