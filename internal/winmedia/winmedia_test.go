package winmedia

import (
	"errors"
	"strings"
	"testing"
)

func TestHR(t *testing.T) {
	cases := map[int32]string{
		DXGIErrorAccessLost:  "0x887A0026 (DXGI_ERROR_ACCESS_LOST)",
		DXGIErrorWaitTimeout: "0x887A0027 (DXGI_ERROR_WAIT_TIMEOUT)",
		MFETransformNeedMore: "0xC00D6D72 (MF_E_TRANSFORM_NEED_MORE_INPUT)",
		MFEShutdown:          "0xC00D3E85 (MF_E_SHUTDOWN)",
		EAccessDenied:        "0x80070005 (E_ACCESSDENIED)",
		0x12345:              "0x00012345",
	}
	for hr, want := range cases {
		if got := HR(hr); got != want {
			t.Errorf("HR(%d) = %q, want %q", hr, got, want)
		}
	}
}

func TestCheck(t *testing.T) {
	if Check("x", 0) != nil || Check("x", 1) != nil {
		t.Fatal("success HRESULT reported as error")
	}
	err := Check("AcquireNextFrame", DXGIErrorAccessLost)
	var he *HRError
	if !errors.As(err, &he) || he.HR != DXGIErrorAccessLost || !strings.Contains(err.Error(), "AcquireNextFrame: 0x887A0026") {
		t.Fatalf("Check = %v", err)
	}
}

func TestLogger(t *testing.T) {
	var got []string
	SetLogger(func(s string) { got = append(got, s) })
	defer SetLogger(nil)
	Logf("a %d", 1)
	if len(got) != 1 || got[0] != "a 1" {
		t.Fatalf("got %q", got)
	}
}
