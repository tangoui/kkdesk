//go:build windows && amd64 && cgo

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type spikeService struct {
	dir    string
	logger *log.Logger
}

func (s spikeService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	running := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.watch(ctx) }()
	status <- running
	for {
		select {
		case err := <-done:
			s.logger.Printf("service finished: %v", err)
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- running
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 5000}
				cancel()
				s.logger.Printf("service stopped: %v", <-done)
				return false, 0
			}
		}
	}
}

func (s spikeService) watch(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lockTime time.Time
	var previous uint32 = ^uint32(0)
	var priorState int
	s.logger.Print("waiting for lock in the active physical console session")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			session := windows.WTSGetActiveConsoleSessionId()
			state, result := lockState(session)
			if previous != session || priorState != state {
				s.logger.Printf("consoleSession=%d lockState=%d queryError=%d", session, state, result)
				previous, priorState = session, state
				lockTime = time.Time{}
			}
			if state != lockedState {
				lockTime = time.Time{}
				continue
			}
			if lockTime.IsZero() {
				lockTime = time.Now()
			}
			if time.Since(lockTime) >= 7*time.Second {
				return s.launch(ctx, session)
			}
		}
	}
}

func (s spikeService) launch(ctx context.Context, session uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var original, primary windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE, &original); err != nil {
		return fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer original.Close()
	access := uint32(windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID)
	if err := windows.DuplicateTokenEx(original, access, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer primary.Close()
	if err := windows.SetTokenInformation(primary, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session))); err != nil {
		return fmt.Errorf("SetTokenInformation(TokenSessionId): %w", err)
	}
	exe := exePath(s.dir)
	app, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{exe, "-helper"}))
	if err != nil {
		return err
	}
	currentDir, err := windows.UTF16PtrFromString(s.dir)
	if err != nil {
		return err
	}
	desktop, _ := windows.UTF16PtrFromString(`winsta0\winlogon`)
	startup := windows.StartupInfo{Desktop: desktop}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	err = windows.CreateProcessAsUser(primary, app, command, nil, nil, false, windows.CREATE_NO_WINDOW, nil, currentDir, &startup, &process)
	if err != nil {
		return fmt.Errorf("CreateProcessAsUser session %d: %w", session, err)
	}
	windows.CloseHandle(process.Thread)
	defer windows.CloseHandle(process.Process)
	s.logger.Printf("helper pid=%d session=%d", process.ProcessId, session)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		wait, err := windows.WaitForSingleObject(process.Process, 200)
		if err != nil {
			return errors.Join(err, terminateHelper(process.Process))
		}
		if wait == windows.WAIT_OBJECT_0 {
			var exit uint32
			if err := windows.GetExitCodeProcess(process.Process, &exit); err != nil {
				return err
			}
			if exit != 0 {
				return fmt.Errorf("helper exit=%d (see helper.log)", exit)
			}
			return nil
		}
	}
	return errors.Join(errors.New("helper expired or was cancelled"), terminateHelper(process.Process))
}

func terminateHelper(process windows.Handle) error {
	if err := windows.TerminateProcess(process, 1); err != nil {
		return err
	}
	wait, err := windows.WaitForSingleObject(process, 2000)
	if err == nil && wait != windows.WAIT_OBJECT_0 {
		return errors.New("helper did not exit after termination")
	}
	return err
}
