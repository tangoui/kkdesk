package lockscreen

import "errors"

type macCodeIdentity struct {
	team       string
	identifier string
}

type macPeer struct {
	uid  uint32
	code macCodeIdentity
}

func macPeerAllowed(self macCodeIdentity, peer macPeer, uid uint32) bool {
	return self.team != "" && self.identifier != "" && peer.uid == uid && peer.code == self
}

func macConsoleAllows(expected, actual uint32, locked bool) bool {
	return expected != 0 && expected != ^uint32(0) && expected == actual && locked
}

const macFrameSlotBytes = 5120 * 2880 * 3 / 2

func macFrameBounds(w, h uint32, sequence uint64) (offset, size int, ok bool) {
	if sequence == 0 || w == 0 || h == 0 || w > 5120 || h > 5120 || w%2 != 0 || h%2 != 0 {
		return 0, 0, false
	}
	size = int(w*h) * 3 / 2
	if size > macFrameSlotBytes {
		return 0, 0, false
	}
	return int(sequence%2) * macFrameSlotBytes, size, true
}

const (
	macNotRegistered = iota
	macEnabled
	macNeedsApproval
	macNotFound
)

func macServiceState(daemon, agent int) ComponentState {
	switch {
	case daemon < 0 || agent < 0:
		return ComponentUnsupported
	case daemon == macNeedsApproval || agent == macNeedsApproval:
		return ComponentNeedsApproval
	case daemon == macEnabled && agent == macEnabled:
		return ComponentRunning
	case daemon == macEnabled || agent == macEnabled:
		return ComponentStopped
	default:
		return ComponentAbsent
	}
}

func changeMacJobs(install bool, status func(agent bool) int, change func(agent, install bool) error) error {
	present := func(s int) bool { return s == macEnabled || s == macNeedsApproval }
	if !install {
		var err error
		for _, agent := range []bool{true, false} {
			if present(status(agent)) {
				err = errors.Join(err, change(agent, false))
			}
		}
		return err
	}
	// SMAppService fails a registration that needs the user's approval
	// (EPERM) yet leaves the job waiting for it: that is not a failure.
	register := func(agent bool) error {
		err := change(agent, true)
		if err != nil && status(agent) == macNeedsApproval {
			return nil
		}
		return err
	}
	newDaemon := !present(status(false))
	if newDaemon {
		if err := register(false); err != nil {
			return err
		}
	}
	if !present(status(true)) {
		if err := register(true); err != nil {
			if newDaemon {
				err = errors.Join(err, change(false, false))
			}
			return err
		}
	}
	return nil
}
