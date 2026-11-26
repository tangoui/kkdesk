//go:build windows && cgo

package winmedia

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall -Wno-unknown-pragmas
#cgo windows LDFLAGS: -ld3d11 -ldxgi -lmfplat -lmf -lmfuuid -lole32 -luuid -luser32 -static-libgcc -static-libstdc++ -static
#include <stdlib.h>
#include "winmedia_windows.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

//export mmwmLog
func mmwmLog(msg *C.char) { Logf("%s", C.GoString(msg)) }

var dev struct {
	mu      sync.Mutex
	device  unsafe.Pointer // ID3D11Device*
	manager unsafe.Pointer // IMFDXGIDeviceManager*
	luid    uint64
	gen     uint64
	// retired holds replaced devices and managers: never released, because
	// another thread may have just been handed the pointers (Device returns
	// borrowed references). Device loss is rare; each costs one dead device.
	retired []unsafe.Pointer
}

// ComInit initializes COM (MTA) on the calling OS thread. Callers that run
// a loop on a goroutine should runtime.LockOSThread first.
func ComInit() { C.mmwm_com_init() }

// Device returns the process-wide ID3D11Device* and IMFDXGIDeviceManager*
// (borrowed references, valid until the device is lost) and a generation
// number that changes whenever the device is re-created. The device is
// created on first use; a removed device (driver update, TDR) is replaced.
func Device() (device, manager uintptr, gen uint64, err error) {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	if dev.device != nil {
		hr := int32(C.mmwm_device_removed_reason(dev.device))
		if hr == 0 {
			return uintptr(dev.device), uintptr(dev.manager), dev.gen, nil
		}
		Logf("D3D11 device removed (%s); creating a new one", HR(hr))
		// Old textures and encoders still hold references and release them
		// when closed; this reference is kept (see retired).
		dev.retired = append(dev.retired, dev.manager, dev.device)
		dev.device, dev.manager = nil, nil
	}
	var d, m unsafe.Pointer
	var luid C.uint64_t
	if hr := int32(C.mmwm_create_device(&d, &m, &luid)); hr < 0 {
		return 0, 0, 0, fmt.Errorf("winmedia: create D3D11 device: %s", HR(hr))
	}
	dev.device, dev.manager, dev.luid = d, m, uint64(luid)
	dev.gen++
	return uintptr(d), uintptr(m), dev.gen, nil
}

// AdapterLUID returns the LUID (HighPart<<32 | LowPart) of the adapter
// the device was created on, 0 before the first Device call.
func AdapterLUID() uint64 {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return dev.luid
}

// Release calls IUnknown::Release on a COM pointer (0 is ignored).
func Release(p uintptr) { C.mmwm_release(Ptr(p)) }

// Ptr converts a COM pointer held as uintptr back to a pointer for passing
// to C. The referenced memory is never Go memory.
func Ptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// NewNV12 creates a w×h NV12 render-target texture on device and returns
// the ID3D11Texture2D* (release with Release).
func NewNV12(device uintptr, w, h int) (uintptr, error) {
	var t unsafe.Pointer
	if hr := int32(C.mmwm_new_nv12(Ptr(device), C.int(w), C.int(h), &t)); hr < 0 {
		return 0, Check("CreateTexture2D(NV12)", hr)
	}
	return uintptr(t), nil
}

// Converter is a D3D11 video processor converting an RGB texture (the
// desktop image) to NV12 at another size, BT.709 limited range. It is not
// safe for concurrent use.
type Converter struct {
	c *C.mmwmConv
}

// NewConverter creates a converter for inW×inH textures of DXGI format
// inFormat to outW×outH NV12; rotation is the DXGI_MODE_ROTATION of the
// display (the input is in the panel's native orientation).
func NewConverter(device uintptr, inW, inH, inFormat, outW, outH, rotation int) (*Converter, error) {
	var c *C.mmwmConv
	hr := int32(C.mmwm_conv_create(Ptr(device), C.int(inW), C.int(inH), C.int(inFormat), C.int(outW), C.int(outH),
		C.int(rotation), &c))
	if hr < 0 || c == nil {
		return nil, Check("create video processor", hr)
	}
	return &Converter{c: c}, nil
}

// Convert renders src (RGB texture) into dst (NV12 texture created with
// NewNV12). Views are cached per texture.
func (c *Converter) Convert(src, dst uintptr) error {
	if c == nil || c.c == nil {
		return errors.New("winmedia: converter closed")
	}
	return Check("VideoProcessorBlt", int32(C.mmwm_conv_blt(c.c, Ptr(src), Ptr(dst))))
}

// Close releases the video processor and its cached views.
func (c *Converter) Close() {
	if c == nil || c.c == nil {
		return
	}
	C.mmwm_conv_destroy(c.c)
	c.c = nil
}
