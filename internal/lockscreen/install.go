package lockscreen

import "errors"

type ComponentState uint8

const (
	ComponentUnsupported ComponentState = iota
	ComponentAbsent
	ComponentRunning
	ComponentStopped
	ComponentNeedsUpdate
)

type ComponentInfo struct {
	State         ComponentState
	InstalledPath string
	UsingInstall  bool
}

type installStep struct {
	apply func() error
	undo  func() error
}

// A step's undo also handles its own partial failure.
func installComponent(steps []installStep) error {
	for i, step := range steps {
		if err := step.apply(); err != nil {
			for ; i >= 0; i-- {
				if steps[i].undo != nil {
					err = errors.Join(err, steps[i].undo())
				}
			}
			return err
		}
	}
	return nil
}

type installActions struct {
	stage, discard, stop, replace, restore, register, unregister, start func() error
}

func (a installActions) install(registered, running, current bool) error {
	if registered && current {
		if running {
			return nil
		}
		return a.start()
	}
	steps := []installStep{{apply: a.stage, undo: a.discard}}
	if registered && running {
		steps = append(steps, installStep{apply: a.stop, undo: a.start})
	}
	steps = append(steps, installStep{apply: a.replace, undo: a.restore})
	if !registered {
		steps = append(steps, installStep{apply: a.register, undo: a.unregister})
	}
	steps = append(steps, installStep{apply: a.start, undo: a.stop})
	return installComponent(steps)
}

type uninstallActions struct {
	stop, removeService, removeFiles func() error
}

func (a uninstallActions) uninstall(registered bool) error {
	if registered {
		if err := a.stop(); err != nil {
			return err
		}
		if err := a.removeService(); err != nil {
			return err
		}
	}
	return a.removeFiles()
}
