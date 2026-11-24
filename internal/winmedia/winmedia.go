// Package winmedia holds what the Windows capture and encoder backends
// share: one process-wide Direct3D 11 device (on the adapter that owns the
// primary display, multithread protected, wrapped in an
// IMFDXGIDeviceManager for Media Foundation), a D3D11 video processor that
// converts and scales the desktop image to NV12, HRESULT formatting and a
// logger. Only the logger and HRESULT helpers exist on other platforms.
package winmedia

import (
	"fmt"
	"log"
	"sync/atomic"
)

var logger atomic.Pointer[func(string)]

// SetLogger directs the backend diagnostics (device, capture, encoder) to
// f. nil restores the default (log.Printf with a "mmdesk win:" prefix).
func SetLogger(f func(string)) {
	if f == nil {
		logger.Store(nil)
		return
	}
	logger.Store(&f)
}

// Logf writes one diagnostic line.
func Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if f := logger.Load(); f != nil {
		(*f)(msg)
		return
	}
	log.Printf("mmdesk win: %s", msg)
}

// HRESULTs the backends branch on or that are common in their logs.
const (
	SOK                     int32 = 0
	DXGIErrorAccessLost     int32 = -2005270490 // 0x887A0026
	DXGIErrorWaitTimeout    int32 = -2005270489 // 0x887A0027
	DXGIErrorDeviceRemoved  int32 = -2005270523 // 0x887A0005
	DXGIErrorDeviceReset    int32 = -2005270521 // 0x887A0007
	DXGIErrorInvalidCall    int32 = -2005270527 // 0x887A0001
	DXGIErrorUnsupported    int32 = -2005270524 // 0x887A0004
	DXGIErrorNotCurrently   int32 = -2005270494 // 0x887A0022
	DXGIErrorSessionDiscon  int32 = -2005270488 // 0x887A0028
	DXGIErrorAccessDenied   int32 = -2005270485 // 0x887A002B
	EAccessDenied           int32 = -2147024891 // 0x80070005
	EInvalidArg             int32 = -2147024809 // 0x80070057
	EOutOfMemory            int32 = -2147024882 // 0x8007000E
	ENotImpl                int32 = -2147467263 // 0x80004001
	EFail                   int32 = -2147467259 // 0x80004005
	EPointer                int32 = -2147467261 // 0x80004003
	MFENotAccepting         int32 = -1072875851 // 0xC00D36B5 MF_E_NOTACCEPTING
	MFETransformNeedMore    int32 = -1072861838 // 0xC00D6D72 MF_E_TRANSFORM_NEED_MORE_INPUT
	MFETransformStreamChg   int32 = -1072861855 // 0xC00D6D61 MF_E_TRANSFORM_STREAM_CHANGE
	MFEShutdown             int32 = -1072873851 // 0xC00D3E85 MF_E_SHUTDOWN
	MFENoEventsAvailable    int32 = -1072873856 // 0xC00D3E80 MF_E_NO_EVENTS_AVAILABLE
	MFEInvalidMediaType     int32 = -1072875852 // 0xC00D36B4 MF_E_INVALIDMEDIATYPE
	MFETransformTypeNotSet  int32 = -1072861856 // 0xC00D6D60 MF_E_TRANSFORM_TYPE_NOT_SET
	MFEUnsupportedD3DType   int32 = -1072861834 // 0xC00D6D76 MF_E_UNSUPPORTED_D3D_TYPE
	MFEAsyncLocked          int32 = -1072861833 // 0xC00D6D77 MF_E_TRANSFORM_ASYNC_LOCKED
	MFEHWMFTFailedStartStrm int32 = -1072875772 // 0xC00D3704 MF_E_HW_MFT_FAILED_START_STREAMING
)

var hrNames = map[int32]string{
	SOK:                     "S_OK",
	DXGIErrorAccessLost:     "DXGI_ERROR_ACCESS_LOST",
	DXGIErrorWaitTimeout:    "DXGI_ERROR_WAIT_TIMEOUT",
	DXGIErrorDeviceRemoved:  "DXGI_ERROR_DEVICE_REMOVED",
	DXGIErrorDeviceReset:    "DXGI_ERROR_DEVICE_RESET",
	DXGIErrorInvalidCall:    "DXGI_ERROR_INVALID_CALL",
	DXGIErrorUnsupported:    "DXGI_ERROR_UNSUPPORTED",
	DXGIErrorNotCurrently:   "DXGI_ERROR_NOT_CURRENTLY_AVAILABLE",
	DXGIErrorSessionDiscon:  "DXGI_ERROR_SESSION_DISCONNECTED",
	DXGIErrorAccessDenied:   "DXGI_ERROR_ACCESS_DENIED",
	EAccessDenied:           "E_ACCESSDENIED",
	EInvalidArg:             "E_INVALIDARG",
	EOutOfMemory:            "E_OUTOFMEMORY",
	ENotImpl:                "E_NOTIMPL",
	EFail:                   "E_FAIL",
	EPointer:                "E_POINTER",
	MFENotAccepting:         "MF_E_NOTACCEPTING",
	MFETransformNeedMore:    "MF_E_TRANSFORM_NEED_MORE_INPUT",
	MFETransformStreamChg:   "MF_E_TRANSFORM_STREAM_CHANGE",
	MFEShutdown:             "MF_E_SHUTDOWN",
	MFENoEventsAvailable:    "MF_E_NO_EVENTS_AVAILABLE",
	MFEInvalidMediaType:     "MF_E_INVALIDMEDIATYPE",
	MFETransformTypeNotSet:  "MF_E_TRANSFORM_TYPE_NOT_SET",
	MFEUnsupportedD3DType:   "MF_E_UNSUPPORTED_D3D_TYPE",
	MFEAsyncLocked:          "MF_E_TRANSFORM_ASYNC_LOCKED",
	MFEHWMFTFailedStartStrm: "MF_E_HW_MFT_FAILED_START_STREAMING",
}

// HR formats an HRESULT as hex plus its symbolic name when known.
func HR(hr int32) string {
	if n, ok := hrNames[hr]; ok {
		return fmt.Sprintf("0x%08X (%s)", uint32(hr), n)
	}
	return fmt.Sprintf("0x%08X", uint32(hr))
}

// HRError is a failed HRESULT with the operation that returned it.
type HRError struct {
	Op string
	HR int32
}

func (e *HRError) Error() string { return e.Op + ": " + HR(e.HR) }

// Check returns nil for a success HRESULT (>= 0), else an *HRError.
func Check(op string, hr int32) error {
	if hr >= 0 {
		return nil
	}
	return &HRError{Op: op, HR: hr}
}
