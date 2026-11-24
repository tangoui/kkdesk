// Adapter and display enumeration for diagnostics (kkdesk -selftest and
// the host log). Plain C declarations: included from the cgo preamble and
// from sysinfo_windows.cpp.
#ifndef KKDESK_WINMEDIA_SYSINFO_WINDOWS_H
#define KKDESK_WINMEDIA_SYSINFO_WINDOWS_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    uint16_t description[128]; // DXGI_ADAPTER_DESC1.Description (UTF-16, NUL-terminated)
    uint32_t vendorId, deviceId, subSysId, revision;
    uint64_t dedicatedVideoMemory, dedicatedSystemMemory, sharedSystemMemory;
    uint32_t flags;      // DXGI_ADAPTER_FLAG (2 = software)
    uint64_t luid;       // HighPart<<32 | LowPart
    int64_t umdVersion;  // user-mode driver version (CheckInterfaceSupport), 0 if unknown
    int32_t outputs;     // number of outputs attached to the desktop
} mmsiAdapter;

typedef struct {
    uint16_t deviceName[32]; // DXGI_OUTPUT_DESC.DeviceName, e.g. \\.\DISPLAY1
    int32_t adapter;         // index into the adapter list
    int32_t left, top, right, bottom; // desktop coordinates (physical pixels when DPI aware)
    int32_t rotation;        // DXGI_MODE_ROTATION
    int32_t primary;         // 1 for the primary monitor
    uint32_t modeWidth, modeHeight, modeHz, modeBits; // EnumDisplaySettingsW(ENUM_CURRENT_SETTINGS)
    uint32_t dpiX, dpiY;     // GetDpiForMonitor(MDT_EFFECTIVE_DPI), 0 if unavailable
} mmsiOutput;

// Fills up to maxA adapters and maxO outputs; returns an HRESULT and the
// counts written.
int32_t mmsi_enum(mmsiAdapter *a, int maxA, int *na, mmsiOutput *o, int maxO, int *no);

#ifdef __cplusplus
}
#endif

#endif
