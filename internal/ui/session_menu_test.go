package appui

import (
	"slices"
	"testing"

	"github.com/tangoui/tango/sys/menu"

	"github.com/tangoui/kkdesk/internal/app"
)

func TestQualityMenuLabelsAndSelection(t *testing.T) {
	v := app.NewViewer(app.ViewerConfig{Addr: "localhost", FPS: 60}, nil)
	m := menu.New()
	addQualityMenus(m, v, v.State())
	for i, want := range [][]string{
		{"自动", "30 Mbps", "14 Mbps", "8 Mbps", "3 Mbps"},
		{"30 fps", "60 fps", "90 fps", "144 fps"},
	} {
		var labels, checked []string
		for _, item := range m.Items[i].Submenu.Items {
			labels = append(labels, item.Label)
			if item.Checked {
				checked = append(checked, item.Label)
			}
		}
		if !slices.Equal(labels, want) || !slices.Equal(checked, []string{[]string{"自动", "60 fps"}[i]}) {
			t.Fatalf("submenu %d labels %q, selected %q", i, labels, checked)
		}
	}
	m.Items[0].Submenu.Items[3].OnClick()
	if st := v.State(); st.BitrateBps != 8_000_000 || st.FPS != 60 {
		t.Fatalf("8 Mbps selected the wrong quality: %+v", st)
	}
}

func TestPrivacyMenuUsesHostState(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  app.ViewerConfig
		st   app.ViewerState
		want int
	}{
		{"desktop", app.ViewerConfig{}, app.ViewerState{Phase: app.ViewerStreaming}, 3},
		{"view-only", app.ViewerConfig{ViewOnly: true}, app.ViewerState{
			Phase: app.ViewerStreaming, ViewOnly: true, PrivacyScreen: true, LockAfterDisconnect: true,
		}, 3},
		{"files", app.ViewerConfig{FilesOnly: true}, app.ViewerState{Phase: app.ViewerStreaming}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := app.NewViewer(tc.cfg, nil)
			m := menu.New()
			addPrivacyMenu(m, v, tc.st)
			if len(m.Items) != tc.want {
				t.Fatalf("got %d entries, want %d", len(m.Items), tc.want)
			}
			if tc.want == 0 {
				return
			}
			if m.Items[0].Label != "隐私屏" || !m.Items[0].Checkable || m.Items[0].Checked != tc.st.PrivacyScreen {
				t.Fatalf("privacy screen item = %+v", m.Items[0])
			}
			if m.Items[1].Label != "本次断开后锁屏" || !m.Items[1].Checkable || m.Items[1].Checked != tc.st.LockAfterDisconnect {
				t.Fatalf("lock on disconnect item = %+v", m.Items[1])
			}
			if m.Items[2].Label != "锁屏并断开" || m.Items[2].Checkable {
				t.Fatalf("lock now item = %+v", m.Items[2])
			}
		})
	}
}
