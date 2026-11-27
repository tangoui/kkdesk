//go:build windows && amd64 && cgo

package lockscreen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const serviceName = "KKDeskLockScreen"

func Dispatch(args []string) (bool, error) {
	if len(args) == 1 {
		switch args[0] {
		case "-install-service":
			return true, manageWindows(true)
		case "-uninstall-service":
			return true, manageWindows(false)
		}
	}
	servicePID, nonce, helperMode := helperLaunchArgs(args)
	if !helperMode && (len(args) != 1 || args[0] != "-lockscreen-service") {
		return false, nil
	}
	exe, err := checkSystemImage()
	if err != nil {
		return true, err
	}
	out, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "lockscreen-service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return true, err
	}
	defer out.Close()
	logger := log.New(out, "", log.LstdFlags|log.LUTC)
	if helperMode {
		err = runHelper(exe, servicePID, nonce, logger)
	} else {
		err = svc.Run(serviceName, lockService{exe: exe, logger: logger})
	}
	if err != nil {
		logger.Printf("component exited: %v", err)
	}
	return true, err
}

func protectedExe() (string, error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kkdesk", "kkdesk.exe"), nil
}

func checkSystemImage() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	if !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return "", errors.New("lock screen component requires LocalSystem")
	}
	path, err := protectedExe()
	if err != nil {
		return "", err
	}
	actual, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(actual, path) {
		return "", errors.New("lock screen component requires its installed image")
	}
	if err := verifyInstallDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}

type lockService struct {
	exe    string
	logger *log.Logger
}

func (s lockService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watch(ctx)
	}()
	running := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	status <- running
	for {
		req, ok := <-requests
		if !ok {
			cancel()
			<-done
			return false, 0
		}
		switch req.Cmd {
		case svc.Interrogate:
			status <- running
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending, WaitHint: 5000}
			cancel()
			<-done
			return false, 0
		}
	}
}

func (s lockService) watch(ctx context.Context) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	var helper *helperProcess
	defer func() {
		if helper != nil {
			helper.close()
		}
	}()
	var retryAt time.Time
	// Moving the helper's token into the console session needs
	// SeTcbPrivilege; LocalSystem usually has it enabled, but not always.
	s.logger.Printf("SeTcbPrivilege: %s", enableTCB())
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		console := windows.WTSGetActiveConsoleSessionId()
		locked := windowsSessionLocked(console)
		if helper != nil && (!locked || helper.session != console || helper.exited()) {
			helper.close()
			helper = nil
		}
		if helper != nil {
			if result, _ := windows.WaitForSingleObject(helper.sas, 0); result == windows.WAIT_OBJECT_0 {
				if err := sendSAS(console); err != nil {
					s.logger.Printf("SAS session %d: %v", console, err)
				}
			}
		}
		if !locked || helper != nil || time.Now().Before(retryAt) {
			continue
		}
		if _, err := consoleUser(console); err != nil {
			continue
		}
		retryAt = time.Now().Add(2 * time.Second)
		var err error
		helper, err = startHelper(s.exe, console)
		if err != nil {
			s.logger.Printf("helper session %d: %v", console, err)
		}
	}
}

type helperProcess struct {
	session uint32
	proc    windows.Handle
	sas     windows.Handle
}

func (h *helperProcess) close() {
	if !h.exited() {
		_ = windows.TerminateProcess(h.proc, 0)
		_, _ = windows.WaitForSingleObject(h.proc, 2000)
	}
	_ = windows.CloseHandle(h.proc)
	_ = windows.CloseHandle(h.sas)
}

func (h *helperProcess) exited() bool {
	wait, _ := windows.WaitForSingleObject(h.proc, 0)
	return wait != uint32(windows.WAIT_TIMEOUT)
}

// helperTokenAccess is what the helper's copy of the service token needs:
// setting TokenSessionId takes TOKEN_ADJUST_DEFAULT as well as
// TOKEN_ADJUST_SESSIONID.
const helperTokenAccess = windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY |
	windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID

func startHelper(exe string, session uint32) (*helperProcess, error) {
	var original, primary windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE, &original); err != nil {
		return nil, fmt.Errorf("open service token: %w", err)
	}
	defer original.Close()
	if err := windows.DuplicateTokenEx(original, helperTokenAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return nil, fmt.Errorf("duplicate service token: %w", err)
	}
	defer primary.Close()
	if err := windows.SetTokenInformation(primary, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session))); err != nil {
		return nil, fmt.Errorf("move token to session %d (needs SeTcbPrivilege; %s): %w", session, tcbState(), err)
	}
	ready := false
	var nonce [16]byte
	rand.Read(nonce[:])
	sasName := hex.EncodeToString(nonce[:])
	sas, err := createSASEvent(sasName)
	if err != nil {
		return nil, fmt.Errorf("create SAS event: %w", err)
	}
	defer func() {
		if !ready {
			_ = windows.CloseHandle(sas)
		}
	}()
	app, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{exe, "-lockscreen-helper", strconv.Itoa(os.Getpid()), sasName}))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	desktop, _ := windows.UTF16PtrFromString(`winsta0\winlogon`)
	startup := windows.StartupInfo{Desktop: desktop}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var proc windows.ProcessInformation
	if err := windows.CreateProcessAsUser(primary, app, args, nil, nil, false,
		windows.CREATE_NO_WINDOW, nil, dir, &startup, &proc); err != nil {
		return nil, fmt.Errorf("start lock screen helper: %w", err)
	}
	defer windows.CloseHandle(proc.Thread)
	ready = true
	return &helperProcess{session: session, proc: proc.Process, sas: sas}, nil
}

// tcbState reports whether the service's token holds SeTcbPrivilege and
// whether it is enabled (diagnostics for the helper start).
func tcbState() string {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "token unreadable: " + err.Error()
	}
	defer token.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeTcbPrivilege"), &luid); err != nil {
		return "lookup failed: " + err.Error()
	}
	var n uint32
	_ = windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &n)
	if n == 0 {
		return "privileges unreadable"
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buf[0], n, &n); err != nil {
		return "privileges unreadable: " + err.Error()
	}
	privs := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	for _, p := range unsafe.Slice(&privs.Privileges[0], privs.PrivilegeCount) {
		if p.Luid == luid {
			if p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
				return "present, enabled"
			}
			return "present, disabled"
		}
	}
	return "absent"
}

// enableTCB enables SeTcbPrivilege on the service's token if it holds it,
// and reports the state before and after.
func enableTCB() string {
	before := tcbState()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return before + "; open for adjust: " + err.Error()
	}
	defer token.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeTcbPrivilege"), &luid); err != nil {
		return before + "; lookup: " + err.Error()
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	if err := windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil); err != nil {
		return before + "; enable: " + err.Error()
	}
	return "before: " + before + "; after: " + tcbState()
}
