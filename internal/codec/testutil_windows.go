//go:build windows && cgo

package codec

// Test helpers for codec_windows_test.go: cgo is not allowed in _test.go
// files, so the D3D11 texture helpers live here (unexported).

/*
#cgo windows LDFLAGS: -ld3d11
#define COBJMACROS
#include <stdlib.h>
#include <string.h>
#include <d3d11.h>

static HRESULT mmtestNewNV12(void *device, int w, int h, unsigned char y, unsigned char u, unsigned char v, void **out) {
	ID3D11Device *dev = (ID3D11Device *)device;
	int pitch = w;
	unsigned char *buf = (unsigned char *)malloc((size_t)pitch * h * 3 / 2);
	if (!buf) return E_OUTOFMEMORY;
	memset(buf, y, (size_t)pitch * h);
	unsigned char *uv = buf + (size_t)pitch * h;
	for (int i = 0; i < pitch * h / 2; i += 2) {
		uv[i] = u;
		uv[i + 1] = v;
	}
	D3D11_TEXTURE2D_DESC d;
	memset(&d, 0, sizeof d);
	d.Width = w;
	d.Height = h;
	d.MipLevels = 1;
	d.ArraySize = 1;
	d.Format = DXGI_FORMAT_NV12;
	d.SampleDesc.Count = 1;
	d.Usage = D3D11_USAGE_DEFAULT;
	d.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
	D3D11_SUBRESOURCE_DATA init;
	init.pSysMem = buf;
	init.SysMemPitch = pitch;
	init.SysMemSlicePitch = 0;
	ID3D11Texture2D *t = NULL;
	HRESULT hr = ID3D11Device_CreateTexture2D(dev, &d, &init, &t);
	free(buf);
	*out = t;
	return hr;
}

static void mmtestSize(void *tex, int *w, int *h, int *format, int *array) {
	D3D11_TEXTURE2D_DESC d;
	ID3D11Texture2D_GetDesc((ID3D11Texture2D *)tex, &d);
	*w = (int)d.Width;
	*h = (int)d.Height;
	*format = (int)d.Format;
	*array = (int)d.ArraySize;
}

// Reads the Y value at (x, y) of an NV12 texture through a staging copy;
// with chroma=1 reads the U and V values of the 2x2 block at (x, y) instead
// (out[0], out[1]).
static HRESULT mmtestRead(void *tex, int x, int y, int chroma, unsigned char *out) {
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
			unsigned char *p = (unsigned char *)m.pData;
			if (chroma) {
				unsigned char *uv = p + (size_t)m.RowPitch * d.Height + (size_t)(y / 2) * m.RowPitch + (size_t)(x & ~1);
				out[0] = uv[0];
				out[1] = uv[1];
			} else {
				*out = p[(size_t)y * m.RowPitch + x];
			}
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

// newSolidNV12 creates a w×h NV12 texture filled with (y, u, v) on
// winmedia.Device() (release with winmedia.Release).
func newSolidNV12(w, h int, y, u, v uint8) (uintptr, error) {
	dev, _, _, err := winmedia.Device()
	if err != nil {
		return 0, err
	}
	var t unsafe.Pointer
	if hr := int32(C.mmtestNewNV12(winmedia.Ptr(dev), C.int(w), C.int(h), C.uchar(y), C.uchar(u), C.uchar(v), &t)); hr < 0 {
		return 0, fmt.Errorf("CreateTexture2D(NV12): %s", winmedia.HR(hr))
	}
	return uintptr(t), nil
}

// textureInfo returns the size, DXGI format and array size of a texture.
func textureInfo(tex uintptr) (w, h, format, array int) {
	var cw, ch, cf, ca C.int
	C.mmtestSize(winmedia.Ptr(tex), &cw, &ch, &cf, &ca)
	return int(cw), int(ch), int(cf), int(ca)
}

// lumaAt reads the Y value at (x, y) of an NV12 texture.
func lumaAt(tex uintptr, x, y int) (uint8, error) {
	var v C.uchar
	if hr := int32(C.mmtestRead(winmedia.Ptr(tex), C.int(x), C.int(y), 0, &v)); hr < 0 {
		return 0, fmt.Errorf("read NV12: %s", winmedia.HR(hr))
	}
	return uint8(v), nil
}

// chromaAt reads the U and V values covering (x, y) of an NV12 texture.
func chromaAt(tex uintptr, x, y int) (u, v uint8, err error) {
	var uv [2]C.uchar
	if hr := int32(C.mmtestRead(winmedia.Ptr(tex), C.int(x), C.int(y), 1, &uv[0])); hr < 0 {
		return 0, 0, fmt.Errorf("read NV12 chroma: %s", winmedia.HR(hr))
	}
	return uint8(uv[0]), uint8(uv[1]), nil
}
