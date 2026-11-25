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
