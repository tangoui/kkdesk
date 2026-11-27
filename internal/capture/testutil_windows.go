//go:build windows && cgo

package capture

// Test helper for capture_windows_test.go: cgo is not allowed in _test.go
// files.

/*
#cgo windows LDFLAGS: -ld3d11
#define COBJMACROS
#include <d3d11.h>

static void *mmcaptestDevice(void *tex) {
	ID3D11Device *dev = NULL;
	ID3D11Texture2D_GetDevice((ID3D11Texture2D *)tex, &dev);
	if (dev) ID3D11Device_Release(dev); // the texture keeps it alive
	return dev;
}
*/
import "C"

import "github.com/tangoui/kkdesk/internal/winmedia"

// textureDevice returns the ID3D11Device* a texture was created on.
func textureDevice(tex uintptr) uintptr {
	if tex == 0 {
		return 0
	}
	return uintptr(C.mmcaptestDevice(winmedia.Ptr(tex)))
}
