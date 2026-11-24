//go:build windows && cgo

// Diagnostic enumeration of DXGI adapters and their desktop outputs.
#include <windows.h>
#include <dxgi1_2.h>
#include <string.h>

#include "sysinfo_windows.h"

// GetDpiForMonitor lives in shcore.dll (Windows 8.1+); it is loaded at run
// time so the exe does not import shcore.
typedef HRESULT(WINAPI *getDpiForMonitorFn)(HMONITOR, int, UINT *, UINT *);

extern "C" int32_t mmsi_enum(mmsiAdapter *a, int maxA, int *na, mmsiOutput *o, int maxO, int *no) {
    *na = 0;
    *no = 0;
    IDXGIFactory1 *factory = NULL;
    HRESULT hr = CreateDXGIFactory1(__uuidof(IDXGIFactory1), (void **)&factory);
    if (FAILED(hr)) return (int32_t)hr;

    getDpiForMonitorFn getDpi = NULL;
    HMODULE shcore = LoadLibraryW(L"shcore.dll");
    if (shcore) getDpi = (getDpiForMonitorFn)(void *)GetProcAddress(shcore, "GetDpiForMonitor");
    POINT zero = {0, 0};
    HMONITOR primary = MonitorFromPoint(zero, MONITOR_DEFAULTTOPRIMARY);

    for (UINT i = 0; *na < maxA; i++) {
        IDXGIAdapter1 *adapter = NULL;
        if (factory->EnumAdapters1(i, &adapter) == DXGI_ERROR_NOT_FOUND) break;
        DXGI_ADAPTER_DESC1 d;
        memset(&d, 0, sizeof d);
        adapter->GetDesc1(&d);
        mmsiAdapter *x = &a[*na];
        memset(x, 0, sizeof *x);
        memcpy(x->description, d.Description, sizeof x->description);
        x->description[127] = 0;
        x->vendorId = d.VendorId;
        x->deviceId = d.DeviceId;
        x->subSysId = d.SubSysId;
        x->revision = d.Revision;
        x->dedicatedVideoMemory = (uint64_t)d.DedicatedVideoMemory;
        x->dedicatedSystemMemory = (uint64_t)d.DedicatedSystemMemory;
        x->sharedSystemMemory = (uint64_t)d.SharedSystemMemory;
        x->flags = d.Flags;
        x->luid = ((uint64_t)(uint32_t)d.AdapterLuid.HighPart << 32) | d.AdapterLuid.LowPart;
        LARGE_INTEGER umd;
        if (SUCCEEDED(adapter->CheckInterfaceSupport(__uuidof(IDXGIDevice), &umd))) x->umdVersion = umd.QuadPart;

        for (UINT j = 0; *no < maxO; j++) {
            IDXGIOutput *out = NULL;
            if (adapter->EnumOutputs(j, &out) == DXGI_ERROR_NOT_FOUND) break;
            DXGI_OUTPUT_DESC od;
            memset(&od, 0, sizeof od);
            out->GetDesc(&od);
            out->Release();
            if (!od.AttachedToDesktop) continue;
            x->outputs++;
            mmsiOutput *y = &o[*no];
            memset(y, 0, sizeof *y);
            memcpy(y->deviceName, od.DeviceName, sizeof y->deviceName);
            y->deviceName[31] = 0;
            y->adapter = *na;
            y->left = od.DesktopCoordinates.left;
            y->top = od.DesktopCoordinates.top;
            y->right = od.DesktopCoordinates.right;
            y->bottom = od.DesktopCoordinates.bottom;
            y->rotation = (int32_t)od.Rotation;
            y->primary = od.Monitor == primary;
            DEVMODEW dm;
            memset(&dm, 0, sizeof dm);
            dm.dmSize = sizeof dm;
            if (EnumDisplaySettingsW(od.DeviceName, ENUM_CURRENT_SETTINGS, &dm)) {
                y->modeWidth = dm.dmPelsWidth;
                y->modeHeight = dm.dmPelsHeight;
                y->modeHz = dm.dmDisplayFrequency;
                y->modeBits = dm.dmBitsPerPel;
            }
            UINT dx = 0, dy = 0;
            if (getDpi && SUCCEEDED(getDpi(od.Monitor, 0 /* MDT_EFFECTIVE_DPI */, &dx, &dy))) {
                y->dpiX = dx;
                y->dpiY = dy;
            }
            (*no)++;
        }
        adapter->Release();
        (*na)++;
    }
    factory->Release();
    if (shcore) FreeLibrary(shcore);
    return 0;
}
