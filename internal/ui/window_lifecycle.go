package appui

import (
	"sync"

	"github.com/tangoui/tango"
)

type windowLifecycle struct {
	count int
	quit  func()
}

var windowLifecycles sync.Map // *tango.App -> *windowLifecycle

// QuitAfterLastWindow tracks kkdesk windows on the main thread. Tango's
// auxiliary updater window does not count. The caller manages app shutdown.
func QuitAfterLastWindow(a *tango.App, quit func()) func() {
	windowLifecycles.Store(a, &windowLifecycle{quit: quit})
	return func() { windowLifecycles.Delete(a) }
}

func trackWindow(s *tango.Surface) {
	value, ok := windowLifecycles.Load(s.App())
	if !ok {
		return
	}
	windows := value.(*windowLifecycle)
	windows.count++
	s.OnClosed(func() {
		windows.count--
		if windows.count == 0 {
			windows.quit()
		}
	})
}
