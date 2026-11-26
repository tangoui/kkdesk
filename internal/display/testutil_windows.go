//go:build windows && cgo

package display

// Test helpers for display_windows_test.go: cgo is not allowed in _test.go
// files, so the D3D11 texture helpers live here (unexported).

/*
#cgo windows LDFLAGS: -ld3d11 -luuid -ldxguid
#define COBJMACROS
#include <stdlib.h>
#include <string.h>
#include <d3d11.h>
#include <d3d10.h>

static HRESULT mmdtNewTexture(void *device, int w, int h, int nv12, unsigned char y, unsigned char u, unsigned char v,
                              void **out) {
	ID3D11Device *dev = (ID3D11Device *)device;
	D3D11_TEXTURE2D_DESC d;
	memset(&d, 0, sizeof d);
	d.Width = w;
	d.Height = h;
	d.MipLevels = 1;
	d.ArraySize = 1;
	d.Format = nv12 ? DXGI_FORMAT_NV12 : DXGI_FORMAT_B8G8R8A8_UNORM;
	d.SampleDesc.Count = 1;
	d.Usage = D3D11_USAGE_DEFAULT;
	d.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
	ID3D11Texture2D *t = NULL;
	HRESULT hr;
	if (!nv12) {
		hr = ID3D11Device_CreateTexture2D(dev, &d, NULL, &t);
		*out = t;
		return hr;
	}
	unsigned char *buf = (unsigned char *)malloc((size_t)w * h * 3 / 2);
	if (!buf) return E_OUTOFMEMORY;
	memset(buf, y, (size_t)w * h);
	unsigned char *uv = buf + (size_t)w * h;
	for (int i = 0; i < w * h / 2; i += 2) {
		uv[i] = u;
		uv[i + 1] = v;
	}
	D3D11_SUBRESOURCE_DATA init = {buf, (UINT)w, 0};
	hr = ID3D11Device_CreateTexture2D(dev, &d, &init, &t);
	free(buf);
	*out = t;
	return hr;
}

// Creates an NV12 texture on a new hardware device created without
// D3D11_CREATE_DEVICE_VIDEO_SUPPORT (out receives the texture; the device
// is released with it).
static HRESULT mmdtNoVideoNV12(int w, int h, void **out) {
	D3D_DRIVER_TYPE types[] = {D3D_DRIVER_TYPE_WARP, D3D_DRIVER_TYPE_HARDWARE, D3D_DRIVER_TYPE_HARDWARE};
	D3D_FEATURE_LEVEL fl10 = D3D_FEATURE_LEVEL_10_0;
	HRESULT last = E_NOINTERFACE;
	for (int i = 0; i < 3; i++) {
		ID3D11Device *dev = NULL;
		HRESULT hr = D3D11CreateDevice(NULL, types[i], NULL, D3D11_CREATE_DEVICE_BGRA_SUPPORT, i == 2 ? &fl10 : NULL,
		                               i == 2 ? 1 : 0, D3D11_SDK_VERSION, &dev, NULL, NULL);
		if (FAILED(hr)) {
			last = hr;
			continue;
		}
		ID3D11VideoDevice *vd = NULL;
		if (SUCCEEDED(ID3D11Device_QueryInterface(dev, &IID_ID3D11VideoDevice, (void **)&vd))) {
			ID3D11VideoDevice_Release(vd);
			ID3D11Device_Release(dev);
			continue;
		}
		hr = mmdtNewTexture(dev, w, h, 1, 128, 128, 128, out);
		ID3D11Device_Release(dev);
		if (SUCCEEDED(hr)) return hr;
		last = hr;
	}
	return last;
}

// Creates an NV12 texture on a new hardware device with video support,
// separate from winmedia's (out receives the texture; the device is
// released with it).
static HRESULT mmdtOtherDeviceNV12(int w, int h, unsigned char y, void **out) {
	ID3D11Device *dev = NULL;
	HRESULT hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL,
	                               D3D11_CREATE_DEVICE_BGRA_SUPPORT | D3D11_CREATE_DEVICE_VIDEO_SUPPORT, NULL, 0,
	                               D3D11_SDK_VERSION, &dev, NULL, NULL);
	if (FAILED(hr)) return hr;
	ID3D10Multithread *mt = NULL;
	if (SUCCEEDED(ID3D11Device_QueryInterface(dev, &IID_ID3D10Multithread, (void **)&mt))) {
		ID3D10Multithread_SetMultithreadProtected(mt, TRUE);
		ID3D10Multithread_Release(mt);
	}
	hr = mmdtNewTexture(dev, w, h, 1, y, 128, 128, out);
	ID3D11Device_Release(dev);
	return hr;
}

// Reads the BGRA pixel at (x, y) through a staging copy.
static HRESULT mmdtPixel(void *tex, int x, int y, unsigned char bgra[4]) {
	ID3D11Texture2D *src = (ID3D11Texture2D *)tex;
	ID3D11Device *dev = NULL;
	ID3D11DeviceContext *ctx = NULL;
	ID3D11Texture2D *st = NULL;
	D3D11_TEXTURE2D_DESC d;
	ID3D11Texture2D_GetDesc(src, &d);
	ID3D11Texture2D_GetDevice(src, &dev);
	ID3D11Device_GetImmediateContext(dev, &ctx);
	d.Usage = D3D11_USAGE_STAGING;
	d.BindFlags = 0;
	d.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
	d.MiscFlags = 0;
	HRESULT hr = ID3D11Device_CreateTexture2D(dev, &d, NULL, &st);
	if (SUCCEEDED(hr)) {
		ID3D11DeviceContext_CopyResource(ctx, (ID3D11Resource *)st, (ID3D11Resource *)src);
		D3D11_MAPPED_SUBRESOURCE m;
		hr = ID3D11DeviceContext_Map(ctx, (ID3D11Resource *)st, 0, D3D11_MAP_READ, 0, &m);
		if (SUCCEEDED(hr)) {
			memcpy(bgra, (unsigned char *)m.pData + (size_t)y * m.RowPitch + (size_t)x * 4, 4);
			ID3D11DeviceContext_Unmap(ctx, (ID3D11Resource *)st, 0);
		}
		ID3D11Texture2D_Release(st);
	}
	ID3D11DeviceContext_Release(ctx);
	ID3D11Device_Release(dev);
	return hr;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

func newTestTexture(w, h int, nv12 bool, y, u, v uint8) (uintptr, error) {
	dev, _, _, err := winmedia.Device()
	if err != nil {
		return 0, err
	}
	n := C.int(0)
	if nv12 {
		n = 1
	}
	var t unsafe.Pointer
	if hr := int32(C.mmdtNewTexture(winmedia.Ptr(dev), C.int(w), C.int(h), n, C.uchar(y), C.uchar(u), C.uchar(v), &t)); hr < 0 {
		return 0, fmt.Errorf("CreateTexture2D: %s", winmedia.HR(hr))
	}
	return uintptr(t), nil
}

// newSolidNV12 creates a w×h NV12 texture filled with (y, u, v) on
// winmedia.Device() (release with winmedia.Release).
func newSolidNV12(w, h int, y, u, v uint8) (uintptr, error) {
	return newTestTexture(w, h, true, y, u, v)
}

// newBGRA creates a w×h BGRA render target on winmedia.Device().
func newBGRA(w, h int) (uintptr, error) { return newTestTexture(w, h, false, 0, 0, 0) }

// newNoVideoNV12 creates a w×h NV12 texture on a separate device that has
// no video support (ID3D11VideoDevice is unavailable).
func newNoVideoNV12(w, h int) (uintptr, error) {
	var t unsafe.Pointer
	if hr := int32(C.mmdtNoVideoNV12(C.int(w), C.int(h), &t)); hr < 0 {
		return 0, fmt.Errorf("NV12 texture on a device without video support: %s", winmedia.HR(hr))
	}
	return uintptr(t), nil
}

// newOtherDeviceNV12 creates a solid w×h NV12 texture on a second hardware
// device (not winmedia.Device()).
func newOtherDeviceNV12(w, h int, y uint8) (uintptr, error) {
	var t unsafe.Pointer
	if hr := int32(C.mmdtOtherDeviceNV12(C.int(w), C.int(h), C.uchar(y), &t)); hr < 0 {
		return 0, fmt.Errorf("NV12 texture on a second device: %s", winmedia.HR(hr))
	}
	return uintptr(t), nil
}

// pixelAt reads the (r, g, b) value at (x, y) of a BGRA texture.
func pixelAt(tex uintptr, x, y int) (r, g, b uint8, err error) {
	var px [4]C.uchar
	if hr := int32(C.mmdtPixel(winmedia.Ptr(tex), C.int(x), C.int(y), &px[0])); hr < 0 {
		return 0, 0, 0, fmt.Errorf("read BGRA: %s", winmedia.HR(hr))
	}
	return uint8(px[2]), uint8(px[1]), uint8(px[0]), nil
}
