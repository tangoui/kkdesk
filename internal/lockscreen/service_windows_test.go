//go:build windows && amd64 && cgo

package lockscreen

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Setting TokenSessionId needs TOKEN_ADJUST_DEFAULT as well as
// TOKEN_ADJUST_SESSIONID; without it SetTokenInformation fails with
// "Access is denied" and the helper never starts.
func TestHelperTokenCanMoveToTheConsoleSession(t *testing.T) {
	for name, right := range map[string]uint32{
		"TOKEN_ADJUST_DEFAULT":   windows.TOKEN_ADJUST_DEFAULT,
		"TOKEN_ADJUST_SESSIONID": windows.TOKEN_ADJUST_SESSIONID,
		"TOKEN_ASSIGN_PRIMARY":   windows.TOKEN_ASSIGN_PRIMARY,
	} {
		if helperTokenAccess&right == 0 {
			t.Errorf("helper token access lacks %s", name)
		}
	}
}

// The desktop process verifies the helper's pipe through the pipe handle; it
// cannot open the SYSTEM helper process. A pipe this test serves reports this
// session, and is refused because its owner is not LocalSystem.
func TestPipeServerVerificationUsesThePipe(t *testing.T) {
	name, _ := windows.UTF16PtrFromString(`\\.\pipe\kkdesk-lockscreen-test-` + t.Name())
	server, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(server)
	client, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(client)
	var mine uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &mine); err != nil {
		t.Fatal(err)
	}
	if got, err := pipeServerSession(client); err != nil || got != mine {
		t.Fatalf("pipeServerSession = %d, %v; want %d", got, err, mine)
	}
	if err := verifyPipeServer(client, mine); err == nil || !strings.Contains(err.Error(), "not LocalSystem") {
		t.Fatalf("verifyPipeServer on a user-owned pipe = %v, want an owner error", err)
	}
}
