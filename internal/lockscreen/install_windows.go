//go:build windows && amd64 && cgo

package lockscreen

/*
#cgo windows LDFLAGS: -lshell32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shellapi.h>
#include <stdint.h>

static uintptr_t run_elevated(const WCHAR *exe, const WCHAR *args, DWORD *status) {
	SHELLEXECUTEINFOW e = {0};
	e.cbSize = sizeof(e);
	e.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC;
	e.lpVerb = L"runas";
	e.lpFile = exe;
	e.lpParameters = args;
	e.nShow = SW_SHOWNORMAL;
	if (!ShellExecuteExW(&e)) {
		*status = GetLastError();
		return 0;
	}
	return (uintptr_t)e.hProcess;
}
*/
import "C"

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const installDACL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FRFX;;;BU)"

func Component() (ComponentInfo, error) {
	path, err := protectedExe()
	if err != nil {
		return ComponentInfo{}, err
	}
	info := ComponentInfo{State: ComponentAbsent, InstalledPath: path}
	source, err := os.Executable()
	if err != nil {
		return info, err
	}
	info.UsingInstall = strings.EqualFold(source, path)
	service, err := openQueryService()
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	defer service.Close()
	if err := verifyService(service, path); err != nil {
		return info, err
	}
	status, err := service.Query()
	if err != nil {
		return info, err
	}
	same, err := sameImage(source, path)
	if err != nil {
		return info, err
	}
	switch {
	case !same:
		info.State = ComponentNeedsUpdate
	case status.State == svc.Running:
		info.State = ComponentRunning
	default:
		info.State = ComponentStopped
	}
	return info, nil
}

func openQueryService() (*mgr.Service, error) {
	control, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(control)
	name, _ := windows.UTF16PtrFromString(serviceName)
	h, err := windows.OpenService(control, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return nil, err
	}
	return &mgr.Service{Name: serviceName, Handle: h}, nil
}

func ManageComponent(install bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := "-uninstall-service"
	if install {
		command = "-install-service"
	}
	image, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(command)
	var code C.DWORD
	handle := windows.Handle(C.run_elevated((*C.WCHAR)(unsafe.Pointer(image)), (*C.WCHAR)(unsafe.Pointer(args)), &code))
	if handle == 0 {
		return syscall.Errno(code)
	}
	defer windows.CloseHandle(handle)
	if _, err := windows.WaitForSingleObject(handle, windows.INFINITE); err != nil {
		return err
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(handle, &exit); err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("锁屏远控组件安装程序退出，代码 %d", exit)
	}
	return nil
}

func verifyService(service *mgr.Service, path string) error {
	cfg, err := service.Config()
	if err != nil {
		return err
	}
	cmd := windows.ComposeCommandLine([]string{path, "-lockscreen-service"})
	if !strings.EqualFold(cfg.BinaryPathName, cmd) ||
		(cfg.ServiceStartName != "" && !strings.EqualFold(cfg.ServiceStartName, "LocalSystem")) || cfg.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
		return errors.New("unexpected lock screen service configuration")
	}
	return nil
}

func verifyInstallDirectory(dir string) error {
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var attrs windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &attrs); err != nil {
		return err
	}
	if attrs.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attrs.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("lock screen component directory is not a protected directory")
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	expected, err := windows.SecurityDescriptorFromString(installDACL)
	if err != nil {
		return err
	}
	_, dacl, ok := strings.Cut(sd.String(), "D:")
	_, wanted, _ := strings.Cut(expected.String(), "D:")
	if owner == nil || (!owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) && !owner.IsWellKnown(windows.WinLocalSystemSid)) ||
		!ok || dacl != wanted {
		return errors.New("lock screen component directory has unexpected ownership or permissions")
	}
	return nil
}

func prepareInstallDirectory(dir string) error {
	name, _ := windows.UTF16PtrFromString(dir)
	sd, err := windows.SecurityDescriptorFromString("O:BA" + installDACL)
	if err != nil {
		return err
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, &attrs); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	return verifyInstallDirectory(dir)
}

func sameImage(a, b string) (bool, error) {
	hash := func(path string) ([]byte, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return nil, err
		}
		return h.Sum(nil), nil
	}
	left, err := hash(a)
	if err != nil {
		return false, err
	}
	right, err := hash(b)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return string(left) == string(right), err
}

func managementLock() (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Global\KKDeskLockScreenManagement`)
	sd, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	mutex, err := windows.CreateMutex(&sa, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	result, err := windows.WaitForSingleObject(mutex, 15000)
	if err != nil || (result != windows.WAIT_OBJECT_0 && result != windows.WAIT_ABANDONED) {
		_ = windows.CloseHandle(mutex)
		return nil, errors.New("another lock screen component operation is running")
	}
	return func() {
		_ = windows.ReleaseMutex(mutex)
		_ = windows.CloseHandle(mutex)
	}, nil
}

func manageWindows(install bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	control, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer control.Disconnect()
	release, err := managementLock()
	if err != nil {
		return err
	}
	defer release()
	path, err := protectedExe()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	service, err := control.OpenService(serviceName)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return err
	}
	if service != nil {
		if err := verifyService(service, path); err != nil {
			service.Close()
			return err
		}
	}
	defer func() {
		if service != nil {
			_ = service.Close()
		}
	}()
	if !install {
		return removeWindows(dir, service)
	}
	if err := prepareInstallDirectory(dir); err != nil {
		return err
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	return installWindows(control, &service, source, path)
}

func installWindows(control *mgr.Mgr, service **mgr.Service, source, path string) error {
	same, err := sameImage(source, path)
	if err != nil {
		return err
	}
	job := *service
	var running bool
	if job != nil {
		status, err := job.Query()
		if err != nil {
			return err
		}
		running = status.State != svc.Stopped
	}
	var stage string
	backup := path + ".previous"
	movedOld, movedNew := false, false
	ops := installActions{
		stage: func() error {
			f, err := os.CreateTemp(filepath.Dir(path), "kkdesk.new-*")
			if err != nil {
				return err
			}
			stage = f.Name()
			in, err := os.Open(source)
			if err != nil {
				_ = f.Close()
				return err
			}
			defer in.Close()
			_, err = io.Copy(f, in)
			return errors.Join(err, f.Sync(), f.Close())
		},
		discard: func() error { return removeIfExists(stage) },
		stop: func() error {
			if job == nil {
				return nil
			}
			return stopWindowsService(job)
		},
		replace: func() error {
			if _, err := os.Stat(backup); err == nil {
				return errors.New("a previous component image is pending cleanup")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			err := os.Rename(path, backup)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			movedOld = err == nil
			if err := os.Rename(stage, path); err != nil {
				return err
			}
			movedNew = true
			return nil
		},
		restore: func() error {
			if movedNew {
				if err := removeIfExists(path); err != nil {
					return err
				}
			}
			if movedOld {
				return os.Rename(backup, path)
			}
			return nil
		},
		register: func() error {
			var err error
			job, err = control.CreateService(serviceName, path, mgr.Config{
				StartType:   mgr.StartAutomatic,
				DisplayName: "kkdesk Lock Screen",
				Description: "Supports active kkdesk remote sessions on the locked local console",
			}, "-lockscreen-service")
			*service = job
			return err
		},
		unregister: func() error {
			if job == nil {
				return nil
			}
			err := errors.Join(job.Delete(), job.Close())
			job, *service = nil, nil
			return err
		},
		start: func() error {
			status, err := job.Query()
			if err != nil {
				return err
			}
			if status.State == svc.Stopped {
				if err := job.Start(); err != nil {
					return err
				}
			}
			return waitWindowsService(job, svc.Running)
		},
	}
	if err := ops.install(job != nil, running, same); err != nil {
		return err
	}
	if movedOld {
		return removeInstalledImage(backup)
	}
	return nil
}

func stopWindowsService(job *mgr.Service) error {
	status, err := job.Query()
	if err != nil || status.State == svc.Stopped {
		return err
	}
	if status.State != svc.StopPending {
		if _, err := job.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
	}
	return waitWindowsService(job, svc.Stopped)
}

func waitWindowsService(job *mgr.Service, state svc.State) error {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		status, err := job.Query()
		if err != nil || status.State == state {
			return err
		}
		if state == svc.Running && status.State == svc.Stopped {
			return errors.New("lock screen service exited during startup")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("lock screen service operation timed out")
}

func removeWindows(dir string, service *mgr.Service) error {
	dirErr := verifyInstallDirectory(dir)
	if errors.Is(dirErr, os.ErrNotExist) && service == nil {
		return nil
	}
	if dirErr != nil {
		return dirErr
	}
	ops := uninstallActions{
		stop:          func() error { return stopWindowsService(service) },
		removeService: serviceDelete(service),
		removeFiles: func() error {
			for _, name := range []string{"kkdesk.exe", "kkdesk.exe.previous", "lockscreen-service.log"} {
				if err := removeInstalledImage(filepath.Join(dir, name)); err != nil {
					return err
				}
			}
			err := removeIfExists(dir)
			if errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) {
				return scheduleRemoval(dir)
			}
			return err
		},
	}
	return ops.uninstall(service != nil)
}

func serviceDelete(service *mgr.Service) func() error {
	return func() error { return service.Delete() }
}

func removeIfExists(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func removeInstalledImage(path string) error {
	err := removeIfExists(path)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		var id [16]byte
		rand.Read(id[:])
		retired := filepath.Join(filepath.Dir(path), "kkdesk.retired-"+hex.EncodeToString(id[:]))
		if renameErr := os.Rename(path, retired); renameErr != nil {
			return errors.Join(err, renameErr)
		}
		return scheduleRemoval(retired)
	}
	return err
}

func scheduleRemoval(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(name, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}
