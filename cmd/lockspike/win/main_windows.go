//go:build windows && amd64 && cgo

package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "KKDeskLockSpike"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: kkdesk-lockspike.exe -spike-service-install | -spike-service-uninstall")
	}
	switch args[0] {
	case "-spike-service-install":
		return install()
	case "-spike-service-uninstall":
		return uninstall()
	case "-service":
		dir, err := checkSystemProcess()
		if err != nil {
			return err
		}
		logger, file, err := openLog(dir, "service.log")
		if err != nil {
			return err
		}
		defer file.Close()
		return svc.Run(serviceName, spikeService{dir: dir, logger: logger})
	case "-helper":
		dir, err := checkSystemProcess()
		if err != nil {
			return err
		}
		logger, file, err := openLog(dir, "helper.log")
		if err != nil {
			return err
		}
		defer file.Close()
		err = probe(dir, logger)
		if err != nil {
			logger.Print(err)
		}
		return err
	default:
		return fmt.Errorf("unknown option %q", args[0])
	}
}

func openLog(dir, name string) (*log.Logger, *os.File, error) {
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, err
	}
	return log.New(file, "", log.LstdFlags|log.Lmicroseconds), file, nil
}

func installDir() (string, error) {
	programFiles, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(programFiles, "KKDeskLockSpike"), nil
}

func exePath(dir string) string {
	return filepath.Join(dir, "kkdesk-lockspike.exe")
}

func checkSystemProcess() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	if !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return "", errors.New("service and helper modes require LocalSystem")
	}
	dir, err := installDir()
	if err != nil {
		return "", err
	}
	actual, err := os.Executable()
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(actual, exePath(dir)) {
		return "", fmt.Errorf("run the protected copy at %s", exePath(dir))
	}
	return dir, nil
}

func install() (retErr error) {
	control, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Service Control Manager from elevated PowerShell: %w", err)
	}
	defer control.Disconnect()
	dir, err := installDir()
	if err != nil {
		return err
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	attrs := windows.SecurityAttributes{SecurityDescriptor: sd}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(path, &attrs); err != nil {
		return fmt.Errorf("create protected directory %s (collect old results and uninstall before retrying): %w", dir, err)
	}
	removeFiles := true
	defer func() {
		if retErr != nil && removeFiles {
			retErr = errors.Join(retErr, os.RemoveAll(dir))
		}
	}()
	output, err := os.OpenFile(exePath(dir), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Close()); err != nil {
		return err
	}
	job, err := control.CreateService(serviceName, exePath(dir), mgr.Config{
		StartType:   mgr.StartManual,
		DisplayName: "KKDesk Lock Screen Spike",
		Description: "One-shot local lock screen feasibility probe",
	}, "-service")
	if err != nil {
		return err
	}
	defer job.Close()
	if err := job.Start(); err != nil {
		deleteErr := job.Delete()
		removeFiles = deleteErr == nil
		return errors.Join(err, deleteErr)
	}
	fmt.Printf("Probe armed for 2 minutes. Results will be in %s\n", dir)
	return nil
}

func uninstall() error {
	control, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer control.Disconnect()
	dir, err := installDir()
	if err != nil {
		return err
	}
	job, err := control.OpenService(serviceName)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return err
	}
	if err == nil {
		config, err := job.Config()
		if err != nil {
			job.Close()
			return err
		}
		want := windows.ComposeCommandLine([]string{exePath(dir), "-service"})
		if !strings.EqualFold(config.BinaryPathName, want) {
			job.Close()
			return fmt.Errorf("existing %s service uses a different command: %s", serviceName, config.BinaryPathName)
		}
		if err := stop(job); err != nil {
			job.Close()
			return err
		}
		if err := errors.Join(job.Delete(), job.Close()); err != nil {
			return err
		}
	}
	return removeInstallFiles(dir)
}

func removeInstallFiles(dir string) error {
	entryPath, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(entryPath)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing to remove reparse point %s", dir)
	}
	for _, name := range []string{"kkdesk-lockspike.exe", "service.log", "helper.log", "before.png", "after.png"} {
		err := os.Remove(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Remove(dir)
}

func stop(job *mgr.Service) error {
	status, err := job.Query()
	if err != nil || status.State == svc.Stopped {
		return err
	}
	if status.State != svc.StopPending {
		if _, err := job.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		status, err := job.Query()
		if err != nil || status.State == svc.Stopped {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("timed out stopping the spike service")
}
