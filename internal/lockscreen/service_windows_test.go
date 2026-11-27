//go:build windows && amd64 && cgo

package lockscreen

import (
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
