//go:build windows && amd64 && cgo

package lockscreen

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var sendSASProc = windows.NewLazySystemDLL("sas.dll").NewProc("SendSAS")

func sasEventName(nonce string) *uint16 {
	name, _ := windows.UTF16PtrFromString(`Global\KKDeskLockScreenSAS-` + nonce)
	return name
}

func createSASEvent(nonce string) (windows.Handle, error) {
	sd, err := windows.SecurityDescriptorFromString("O:SYD:P(A;;GA;;;SY)")
	if err != nil {
		return 0, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	return windows.CreateEvent(&sa, 0, 0, sasEventName(nonce))
}

func systemPolicy(name string) (uint64, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, registry.QUERY_VALUE)
	if err != nil {
		return 0, err
	}
	defer key.Close()
	n, _, err := key.GetIntegerValue(name)
	return n, err
}

func sasAllowed() bool {
	n, err := systemPolicy("SoftwareSASGeneration")
	return err == nil && (n == 1 || n == 3)
}

func sasRequired() bool {
	n, err := systemPolicy("DisableCAD")
	return err == nil && n == 0
}

func sendSAS(session uint32) error {
	if !sasAllowed() || windows.WTSGetActiveConsoleSessionId() != session || !windowsSessionLocked(session) {
		return ErrUnavailable
	}
	if err := sendSASProc.Find(); err != nil {
		return err
	}
	var user, impersonation windows.Token
	if err := windows.WTSQueryUserToken(session, &user); err != nil {
		return err
	}
	defer user.Close()
	if err := windows.DuplicateTokenEx(user, windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE,
		nil, windows.SecurityImpersonation, windows.TokenImpersonation, &impersonation); err != nil {
		return err
	}
	defer impersonation.Close()
	runtime.LockOSThread()
	if err := windows.SetThreadToken(nil, impersonation); err != nil {
		runtime.UnlockOSThread()
		return err
	}
	sendSASProc.Call(0) // AsUser=FALSE: caller is the registered service.
	if err := windows.RevertToSelf(); err != nil {
		return errors.Join(errors.New("cannot revert SAS service impersonation"), err)
	}
	runtime.UnlockOSThread()
	return nil
}
