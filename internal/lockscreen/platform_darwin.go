//go:build darwin && cgo

package lockscreen

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework Security -framework ServiceManagement -framework SystemConfiguration -framework CoreGraphics -framework CoreVideo -framework IOKit -lbsm
#include "system_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	macAgentPlist  = "com.kkdesk.lockscreen.agent.plist"
	macDaemonPlist = "com.kkdesk.lockscreen.daemon.plist"
)

func PlatformStart() StartFunc { return startMac }

var (
	macSelfOnce sync.Once
	macSelf     macCodeIdentity
	macJobMu    sync.Mutex
)

func macOwnCode() macCodeIdentity {
	macSelfOnce.Do(func() {
		var identity C.kk_mac_identity
		if C.kk_mac_self(&identity) != 0 {
			macSelf = nativeMacCode(&identity)
		}
	})
	return macSelf
}

func nativeMacCode(identity *C.kk_mac_identity) macCodeIdentity {
	return macCodeIdentity{team: C.GoString(&identity.team[0]), identifier: C.GoString(&identity.identifier[0])}
}

func macAuthorize(conn *net.UnixConn, uid uint32) bool {
	if macOwnCode().team == "" {
		return false
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	ok := false
	err = raw.Control(func(fd uintptr) {
		var identity C.kk_mac_identity
		if C.kk_mac_peer(C.int(fd), &identity) != 0 {
			ok = macPeerAllowed(macOwnCode(), macPeer{uid: uint32(identity.uid), code: nativeMacCode(&identity)}, uid)
		}
	})
	runtime.KeepAlive(conn)
	return err == nil && ok
}

func macConsoleUser() uint32 {
	var uid C.uint32_t
	if C.kk_mac_console(&uid) == 0 {
		return 0
	}
	return uint32(uid)
}

func macLocked(uid uint32) bool {
	return macConsoleAllows(uid, macConsoleUser(), C.kk_mac_console_locked(C.uint32_t(uid)) != 0)
}

func macJobStatus(agent bool) int {
	return int(C.kk_mac_job_status(macBool(agent)))
}

func macBool(v bool) C.int {
	if v {
		return 1
	}
	return 0
}

func Dispatch(args []string) (bool, error) {
	if len(args) != 1 || (args[0] != "-lockscreen-mac-daemon" && args[0] != "-lockscreen-mac-agent") {
		return false, nil
	}
	if macOwnCode().team == "" {
		return true, ErrNeedsSignature
	}
	if os.Getppid() != 1 {
		return true, errors.New("macOS lock screen job requires launchd")
	}
	if args[0] == "-lockscreen-mac-daemon" && os.Geteuid() == 0 {
		return true, runMacDaemon()
	}
	if args[0] == "-lockscreen-mac-agent" && os.Geteuid() != 0 {
		return true, runMacAgent()
	}
	return true, ErrUnavailable
}

func Component() (ComponentInfo, error) {
	if macOwnCode().team == "" {
		return ComponentInfo{State: ComponentNeedsSignature}, nil
	}
	macJobMu.Lock()
	defer macJobMu.Unlock()
	return macComponent()
}

func macComponent() (ComponentInfo, error) {
	exe, err := os.Executable()
	if err != nil {
		return ComponentInfo{}, err
	}
	contents := filepath.Dir(filepath.Dir(exe))
	info := ComponentInfo{InstalledPath: filepath.Dir(contents), UsingInstall: true}
	for i, name := range []string{macDaemonPlist, macAgentPlist} {
		dir := []string{"LaunchDaemons", "LaunchAgents"}[i]
		if _, err := os.Stat(filepath.Join(contents, "Library", dir, name)); err != nil {
			info.State = ComponentUnsupported
			return info, errors.New("当前 kkdesk 安装包没有锁屏远控组件")
		}
	}
	daemon, agent := macJobStatus(false), macJobStatus(true)
	info.State = macServiceState(daemon, agent)
	return info, nil
}

func ManageComponent(install bool) error {
	if macOwnCode().team == "" {
		return ErrNeedsSignature
	}
	macJobMu.Lock()
	defer macJobMu.Unlock()
	info, err := macComponent()
	if err != nil {
		return err
	}
	if info.State == ComponentUnsupported {
		return ErrUnavailable
	}
	return changeMacJobs(install, macJobStatus, func(agent, register bool) error {
		if code := C.kk_mac_job_change(macBool(agent), macBool(register)); code != 0 {
			return fmt.Errorf("macOS 锁屏组件注册状态更改失败（%d）", int(code))
		}
		return nil
	})
}
