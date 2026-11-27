package appui

import (
	"slices"
	"testing"

	"github.com/tangoui/tango"
)

type reopenSurface struct {
	state   tango.WindowState
	actions []string
}

func (s *reopenSurface) State() tango.WindowState { return s.state }
func (s *reopenSurface) Restore() error {
	s.actions = append(s.actions, "restore")
	return nil
}
func (s *reopenSurface) Show()  { s.actions = append(s.actions, "show") }
func (s *reopenSurface) Focus() { s.actions = append(s.actions, "focus") }

func TestPresentMainWindowRestoresOnlyMinimizedWindow(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state tango.WindowState
		want  []string
	}{
		{"minimized", tango.StateMinimized, []string{"restore", "show", "focus"}},
		{"fullscreen", tango.StateFullscreen, []string{"show", "focus"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &reopenSurface{state: tt.state}
			presentMainWindow(s)
			if !slices.Equal(s.actions, tt.want) {
				t.Fatalf("window actions = %v, want %v", s.actions, tt.want)
			}
		})
	}
}
