//go:build windows && amd64 && cgo

package lockscreen

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const pipeClientAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA |
	windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE

var peekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

func pipeConnected(h windows.Handle) bool {
	ok, _, _ := peekNamedPipe.Call(uintptr(h), 0, 0, 0, 0, 0)
	return ok != 0
}

func pipeName(session uint32) *uint16 {
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\kkdesk-lockscreen-1-%d`, session))
	return name
}

func consoleUser(session uint32) (string, error) {
	var token windows.Token
	if err := windows.WTSQueryUserToken(session, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func pipeListener(session uint32, sid string) (windows.Handle, error) {
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:SYD:P(A;;GA;;;SY)(A;;0x%x;;;%s)", pipeClientAccess, sid))
	if err != nil {
		return 0, err
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	return windows.CreateNamedPipe(pipeName(session),
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, &attrs)
}

func waitForClient(h windows.Handle, valid func() bool) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(h, &ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return err
	}
	var count uint32
	for valid() {
		wait, err := windows.WaitForSingleObject(event, 150)
		if err != nil {
			break
		}
		if wait == windows.WAIT_OBJECT_0 {
			return windows.GetOverlappedResult(h, &ov, &count, false)
		}
	}
	_ = windows.CancelIoEx(h, &ov)
	_ = windows.GetOverlappedResult(h, &ov, &count, true)
	return ErrUnavailable
}

func pipeClient(h windows.Handle, identity *localIdentity) (windows.Handle, error) {
	if err := windows.GetNamedPipeClientProcessId(h, &identity.pid); err != nil {
		return 0, err
	}
	if err := windows.ProcessIdToSessionId(identity.pid, &identity.session); err != nil {
		return 0, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_DUP_HANDLE, false, identity.pid)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = windows.CloseHandle(proc)
		}
	}()
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return 0, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return 0, err
	}
	identity.sid = user.User.Sid.String()
	image := make([]uint16, 32768)
	n := uint32(len(image))
	if err := windows.QueryFullProcessImageName(proc, 0, &image[0], &n); err != nil {
		return 0, err
	}
	identity.image = windows.UTF16ToString(image[:n])
	ok = true
	return proc, nil
}

func openPipe(session uint32) (*os.File, error) {
	h, err := windows.CreateFile(pipeName(session), pipeClientAccess, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err == nil {
		owner, _, ownerErr := sd.Owner()
		if ownerErr == nil && owner != nil && owner.IsWellKnown(windows.WinLocalSystemSid) {
			var pid, serverSession uint32
			if windows.GetNamedPipeServerProcessId(h, &pid) == nil &&
				windows.ProcessIdToSessionId(pid, &serverSession) == nil && serverSession == session {
				return os.NewFile(uintptr(h), "kkdesk lock screen"), nil
			}
		}
	}
	_ = windows.CloseHandle(h)
	return nil, ErrUnavailable
}

func receiveCommand(r io.Reader, data []byte) (byte, []byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.LittleEndian.Uint16(header[1:]))
	if n > len(data) {
		return 0, nil, errors.New("lock screen command exceeds message limit")
	}
	data = data[:n]
	if _, err := io.ReadFull(r, data); err != nil {
		clear(data)
		return 0, nil, err
	}
	return header[0], data, nil
}

func sendCommand(w io.Writer, operation byte, data []byte) error {
	if len(data) > maxSecretBytes {
		return errors.New("lock screen command exceeds message limit")
	}
	var frame [3 + maxSecretBytes]byte
	frame[0] = operation
	binary.LittleEndian.PutUint16(frame[1:], uint16(len(data)))
	n := copy(frame[3:], data)
	defer clear(frame[3 : 3+n])
	_, err := w.Write(frame[:3+n])
	return err
}
