//go:build windows && amd64 && cgo

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <wtsapi32.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <wrl/client.h>
#include <cstdlib>
#include <cwchar>

#include "probe_windows.h"

using Microsoft::WRL::ComPtr;

int spike_lock_state(uint32_t session, uint32_t *error) {
    if (session == 0xffffffff) {
        *error = ERROR_NO_SUCH_LOGON_SESSION;
        return -1;
    }
    LPWSTR buffer = nullptr;
    DWORD size = 0;
    if (!WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, session, WTSSessionInfoEx, &buffer, &size)) {
        *error = GetLastError();
        return -1;
    }
    int state = -1;
    auto info = reinterpret_cast<WTSINFOEXW *>(buffer);
    if (size >= sizeof(*info) && info->Level == 1 && info->Data.WTSInfoExLevel1.SessionId == session) {
        state = info->Data.WTSInfoExLevel1.SessionFlags;
    } else {
        *error = ERROR_INVALID_DATA;
    }
    WTSFreeMemory(buffer);
    return state;
}

static bool isWinlogon(HDESK desk, uint32_t *error) {
    wchar_t name[128];
    if (!GetUserObjectInformationW(desk, UOI_NAME, name, sizeof(name), nullptr)) {
        *error = GetLastError();
        return false;
    }
    if (_wcsicmp(name, L"Winlogon") != 0) {
        *error = ERROR_INVALID_STATE;
        return false;
    }
    return true;
}

static HDESK openSecureDesktop(uint32_t session, uint32_t *error) {
    if (WTSGetActiveConsoleSessionId() != session || spike_lock_state(session, error) != WTS_SESSIONSTATE_LOCK) {
        if (!*error) *error = ERROR_INVALID_STATE;
        return nullptr;
    }
    HDESK desk = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS | DESKTOP_WRITEOBJECTS);
    if (!desk) {
        *error = GetLastError();
        return nullptr;
    }
    if (!isWinlogon(desk, error)) {
        CloseDesktop(desk);
        return nullptr;
    }
    return desk;
}

intptr_t spike_enter_desktop(uint32_t session, uint32_t *error) {
    HDESK desk = openSecureDesktop(session, error);
    if (!desk) return 0;
    if (!SetThreadDesktop(desk)) {
        *error = GetLastError();
        CloseDesktop(desk);
        return 0;
    }
    return reinterpret_cast<intptr_t>(desk);
}

void spike_leave_desktop(intptr_t desk) {
    CloseDesktop(reinterpret_cast<HDESK>(desk));
}

int spike_secure_desktop(uint32_t session, uint32_t *error) {
    HDESK desk = openSecureDesktop(session, error);
    if (!desk) return 0;
    CloseDesktop(desk);
    return 1;
}

static HRESULT primaryOutput(ComPtr<IDXGIAdapter1> &adapter, ComPtr<IDXGIOutput1> &primary) {
    ComPtr<IDXGIFactory1> factory;
    HRESULT hr = CreateDXGIFactory1(IID_PPV_ARGS(&factory));
    if (FAILED(hr)) return hr;
    for (UINT a = 0;; ++a) {
        hr = factory->EnumAdapters1(a, adapter.ReleaseAndGetAddressOf());
        if (FAILED(hr)) return hr;
        ComPtr<IDXGIOutput> output;
        for (UINT o = 0;; ++o) {
            hr = adapter->EnumOutputs(o, output.ReleaseAndGetAddressOf());
            if (hr == DXGI_ERROR_NOT_FOUND) break;
            if (FAILED(hr)) return hr;
            DXGI_OUTPUT_DESC desc{};
            hr = output->GetDesc(&desc);
            if (FAILED(hr)) return hr;
            if (desc.AttachedToDesktop && desc.DesktopCoordinates.left == 0 && desc.DesktopCoordinates.top == 0) {
                return output.As(&primary);
            }
        }
    }
}

int32_t spike_capture(uint8_t **rgba, uint32_t *width, uint32_t *height, const char **stage) {
    ComPtr<IDXGIAdapter1> adapter;
    ComPtr<IDXGIOutput1> output;
    *stage = "primary output";
    HRESULT hr = primaryOutput(adapter, output);
    if (FAILED(hr)) return hr;
    ComPtr<ID3D11Device> device;
    ComPtr<ID3D11DeviceContext> context;
    *stage = "D3D11CreateDevice";
    hr = D3D11CreateDevice(adapter.Get(), D3D_DRIVER_TYPE_UNKNOWN, nullptr, D3D11_CREATE_DEVICE_BGRA_SUPPORT,
        nullptr, 0, D3D11_SDK_VERSION, &device, nullptr, &context);
    if (FAILED(hr)) return hr;
    ComPtr<IDXGIOutputDuplication> dup;
    *stage = "DuplicateOutput";
    hr = output->DuplicateOutput(device.Get(), &dup);
    if (FAILED(hr)) return hr;

    *stage = "AcquireNextFrame";
    DXGI_OUTDUPL_FRAME_INFO frame{};
    ComPtr<IDXGIResource> resource;
    ULONGLONG deadline = GetTickCount64() + 3000;
    do {
        UINT remaining = static_cast<UINT>(deadline - GetTickCount64());
        hr = dup->AcquireNextFrame(remaining > 3000 ? 0 : remaining, &frame, resource.ReleaseAndGetAddressOf());
        if (FAILED(hr)) return hr;
        if (frame.LastPresentTime.QuadPart) break;
        resource.Reset();
        dup->ReleaseFrame();
    } while (GetTickCount64() < deadline);
    if (!resource) return DXGI_ERROR_WAIT_TIMEOUT;
    struct ReleaseFrame {
        IDXGIOutputDuplication *dup;
        ~ReleaseFrame() { dup->ReleaseFrame(); }
    } frameGuard{dup.Get()};
    ComPtr<ID3D11Texture2D> texture;
    *stage = "query frame texture";
    hr = resource.As(&texture);
    if (FAILED(hr)) return hr;

    D3D11_TEXTURE2D_DESC desc{};
    texture->GetDesc(&desc);
    *stage = "validate frame";
    if (desc.Format != DXGI_FORMAT_B8G8R8A8_UNORM || !desc.Width || !desc.Height ||
            desc.Width > 16384 || desc.Height > 16384) return E_UNEXPECTED;
    desc.BindFlags = 0;
    desc.MiscFlags = 0;
    desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
    desc.Usage = D3D11_USAGE_STAGING;
    ComPtr<ID3D11Texture2D> staging;
    *stage = "create staging texture";
    hr = device->CreateTexture2D(&desc, nullptr, &staging);
    if (FAILED(hr)) return hr;
    context->CopyResource(staging.Get(), texture.Get());
    D3D11_MAPPED_SUBRESOURCE mapped{};
    *stage = "Map";
    hr = context->Map(staging.Get(), 0, D3D11_MAP_READ, 0, &mapped);
    if (FAILED(hr)) return hr;

    *stage = "allocate RGBA";
    size_t stride = static_cast<size_t>(desc.Width) * 4;
    auto pixels = static_cast<uint8_t *>(std::malloc(stride * desc.Height));
    if (pixels) {
        for (UINT y = 0; y < desc.Height; ++y) {
            const auto *src = static_cast<const uint8_t *>(mapped.pData) + static_cast<size_t>(y) * mapped.RowPitch;
            uint8_t *dst = pixels + static_cast<size_t>(y) * stride;
            for (UINT x = 0; x < desc.Width; ++x, src += 4, dst += 4) {
                dst[0] = src[2];
                dst[1] = src[1];
                dst[2] = src[0];
                dst[3] = 255;
            }
        }
        *rgba = pixels;
        *width = desc.Width;
        *height = desc.Height;
    }
    context->Unmap(staging.Get(), 0);
    return pixels ? S_OK : E_OUTOFMEMORY;
}

unsigned spike_post(uint32_t *error) {
    INPUT keys[6]{};
    for (unsigned i = 0; i < 3; ++i) {
        INPUT &down = keys[i * 2];
        INPUT &up = keys[i * 2 + 1];
        down.type = up.type = INPUT_KEYBOARD;
        down.ki.wVk = up.ki.wVk = static_cast<WORD>('A' + i);
        up.ki.dwFlags = KEYEVENTF_KEYUP;
    }
    SetLastError(ERROR_SUCCESS);
    unsigned sent = SendInput(6, keys, sizeof(INPUT));
    if (sent != 6) *error = GetLastError();
    return sent;
}
