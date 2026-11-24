//go:build windows && cgo

package winmedia

/*
#include "sysinfo_windows.h"
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Adapter describes one DXGI adapter (diagnostics).
type Adapter struct {
	Description                  string
	VendorID, DeviceID, SubSysID uint32
	Revision                     uint32
	DedicatedVideoMemory         uint64
	DedicatedSystemMemory        uint64
	SharedSystemMemory           uint64
	Software                     bool // DXGI_ADAPTER_FLAG_SOFTWARE (Microsoft Basic Render Driver)
	LUID                         uint64
	UMDVersion                   int64 // user-mode driver version, 0 if unknown
	Outputs                      int   // outputs attached to the desktop
}

// DriverVersion formats UMDVersion as a.b.c.d ("" if unknown).
func (a Adapter) DriverVersion() string {
	if a.UMDVersion == 0 {
		return ""
	}
	v := uint64(a.UMDVersion)
	return fmt.Sprintf("%d.%d.%d.%d", v>>48, v>>32&0xffff, v>>16&0xffff, v&0xffff)
}

// Output describes one desktop output (monitor).
type Output struct {
	DeviceName               string
	Adapter                  int // index into the adapter list
	Left, Top, Right, Bottom int // desktop coordinates
	Rotation                 int // DXGI_MODE_ROTATION
	Primary                  bool
	ModeWidth, ModeHeight    int // current display mode
	RefreshHz                int
	BitsPerPixel             int
	DPIX, DPIY               int // effective DPI (96 = 100%), 0 if unknown
}

// EnumAdapters lists the DXGI adapters and their desktop outputs.
func EnumAdapters() ([]Adapter, []Output, error) {
	var ca [16]C.mmsiAdapter
	var co [32]C.mmsiOutput
	var na, no C.int
	if hr := int32(C.mmsi_enum(&ca[0], C.int(len(ca)), &na, &co[0], C.int(len(co)), &no)); hr < 0 {
		return nil, nil, Check("CreateDXGIFactory1", hr)
	}
	as := make([]Adapter, 0, int(na))
	for _, x := range ca[:na] {
		as = append(as, Adapter{
			Description: utf16z(unsafe.Slice((*uint16)(unsafe.Pointer(&x.description[0])), len(x.description))),
			VendorID:    uint32(x.vendorId), DeviceID: uint32(x.deviceId), SubSysID: uint32(x.subSysId),
			Revision:             uint32(x.revision),
			DedicatedVideoMemory: uint64(x.dedicatedVideoMemory), DedicatedSystemMemory: uint64(x.dedicatedSystemMemory),
			SharedSystemMemory: uint64(x.sharedSystemMemory), Software: x.flags&2 != 0, LUID: uint64(x.luid),
			UMDVersion: int64(x.umdVersion), Outputs: int(x.outputs),
		})
	}
	os := make([]Output, 0, int(no))
	for _, y := range co[:no] {
		os = append(os, Output{
			DeviceName: utf16z(unsafe.Slice((*uint16)(unsafe.Pointer(&y.deviceName[0])), len(y.deviceName))),
			Adapter:    int(y.adapter), Left: int(y.left), Top: int(y.top), Right: int(y.right), Bottom: int(y.bottom),
			Rotation: int(y.rotation), Primary: y.primary != 0,
			ModeWidth: int(y.modeWidth), ModeHeight: int(y.modeHeight), RefreshHz: int(y.modeHz),
			BitsPerPixel: int(y.modeBits), DPIX: int(y.dpiX), DPIY: int(y.dpiY),
		})
	}
	return as, os, nil
}

func utf16z(s []uint16) string {
	for i, c := range s {
		if c == 0 {
			return syscall.UTF16ToString(s[:i])
		}
	}
	return syscall.UTF16ToString(s)
}
