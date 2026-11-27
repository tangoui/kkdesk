//go:build windows && amd64 && cgo

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <wtsapi32.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <wrl/client.h>
#include <cstring>
#include <cwchar>
#include <new>

#include "desktop_windows.h"

using Microsoft::WRL::ComPtr;

int kklock_state(uint32_t session) {
    if (session == 0xffffffff) return -1;
    LPWSTR buf = nullptr;
    DWORD bytes = 0;
    if (!WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, session, WTSSessionInfoEx, &buf, &bytes)) return -1;
    auto info = reinterpret_cast<WTSINFOEXW *>(buf);
    int state = -1;
    if (bytes >= sizeof(*info) && info->Level == 1 &&
            info->Data.WTSInfoExLevel1.SessionId == session &&
            info->Data.WTSInfoExLevel1.SessionState == WTSActive) {
        state = info->Data.WTSInfoExLevel1.SessionFlags;
    }
    WTSFreeMemory(buf);
    return state;
}

uintptr_t kklock_open_desktop(uint32_t *error) {
    HDESK desktop = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS | DESKTOP_WRITEOBJECTS);
    *error = desktop ? 0 : GetLastError();
    return reinterpret_cast<uintptr_t>(desktop);
}

int kklock_enter_desktop(uintptr_t desktop, uint32_t *error) {
    BOOL ok = SetThreadDesktop(reinterpret_cast<HDESK>(desktop));
    *error = ok ? 0 : GetLastError();
    return ok;
}

void kklock_close_desktop(uintptr_t desktop) {
    CloseDesktop(reinterpret_cast<HDESK>(desktop));
}

int kklock_desktop_name(uintptr_t desktop, uint16_t *name, uint32_t count) {
    if (!count) return 0;
    name[0] = 0;
    return GetUserObjectInformationW(reinterpret_cast<HDESK>(desktop), UOI_NAME, name,
        static_cast<DWORD>((count - 1) * sizeof(uint16_t)), nullptr);
}

int kklock_is_winlogon(uintptr_t desktop) {
    wchar_t name[128]{};
    return GetUserObjectInformationW(reinterpret_cast<HDESK>(desktop), UOI_NAME, name, sizeof(name), nullptr) &&
        _wcsicmp(name, L"Winlogon") == 0;
}

static INPUT keyInput(WORD key, WORD scan, DWORD flags) {
    INPUT e{};
    e.type = INPUT_KEYBOARD;
    e.ki.wVk = key;
    e.ki.wScan = scan;
    e.ki.dwFlags = flags;
    return e;
}

int kklock_tap(uint16_t key) {
    INPUT keys[] = {keyInput(key, 0, 0), keyInput(key, 0, KEYEVENTF_KEYUP)};
    return SendInput(2, keys, sizeof(INPUT)) == 2;
}

int kklock_password(const uint16_t *password, uint32_t count) {
    if (!count || count > 1024) return 0;
    INPUT keys[2054]{};
    unsigned n = 0;
    keys[n++] = keyInput(VK_CONTROL, 0, 0);
    keys[n++] = keyInput('A', 0, 0);
    keys[n++] = keyInput('A', 0, KEYEVENTF_KEYUP);
    keys[n++] = keyInput(VK_CONTROL, 0, KEYEVENTF_KEYUP);
    for (uint32_t i = 0; i < count; i++) {
        keys[n++] = keyInput(0, password[i], KEYEVENTF_UNICODE);
        keys[n++] = keyInput(0, password[i], KEYEVENTF_UNICODE | KEYEVENTF_KEYUP);
    }
    keys[n++] = keyInput(VK_RETURN, 0, 0);
    keys[n++] = keyInput(VK_RETURN, 0, KEYEVENTF_KEYUP);
    unsigned sent = SendInput(n, keys, sizeof(INPUT));
    SecureZeroMemory(keys, sizeof(keys));
    return sent == n;
}

struct kklock_capture {
    ComPtr<ID3D11Device> device;
    ComPtr<ID3D11DeviceContext> context;
    ComPtr<IDXGIOutputDuplication> duplication;
    ComPtr<ID3D11Texture2D> staging;
    UINT width = 0;
    UINT height = 0;
    UINT rotation = 1;
};

static HRESULT primaryOutput(ComPtr<IDXGIAdapter1> &adapter, ComPtr<IDXGIOutput1> &primary, UINT &rotation) {
    ComPtr<IDXGIFactory1> factory;
    HRESULT hr = CreateDXGIFactory1(IID_PPV_ARGS(&factory));
    if (FAILED(hr)) return hr;
    for (UINT a = 0;; ++a) {
        hr = factory->EnumAdapters1(a, adapter.ReleaseAndGetAddressOf());
        if (FAILED(hr)) return hr;
        for (UINT o = 0;; ++o) {
            ComPtr<IDXGIOutput> output;
            hr = adapter->EnumOutputs(o, &output);
            if (hr == DXGI_ERROR_NOT_FOUND) break;
            if (FAILED(hr)) return hr;
            DXGI_OUTPUT_DESC desc{};
            hr = output->GetDesc(&desc);
            if (FAILED(hr)) return hr;
            if (!desc.AttachedToDesktop || desc.DesktopCoordinates.left || desc.DesktopCoordinates.top) continue;
            rotation = desc.Rotation;
            return output.As(&primary);
        }
    }
}

int32_t kklock_open_capture(kklock_capture **out) {
    auto c = new(std::nothrow) kklock_capture();
    if (!c) return E_OUTOFMEMORY;
    ComPtr<IDXGIAdapter1> adapter;
    ComPtr<IDXGIOutput1> output;
    HRESULT hr = primaryOutput(adapter, output, c->rotation);
    if (SUCCEEDED(hr)) {
        hr = D3D11CreateDevice(adapter.Get(), D3D_DRIVER_TYPE_UNKNOWN, nullptr, D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            nullptr, 0, D3D11_SDK_VERSION, &c->device, nullptr, &c->context);
    }
    if (SUCCEEDED(hr)) hr = output->DuplicateOutput(c->device.Get(), &c->duplication);
    if (FAILED(hr)) {
        delete c;
        return hr;
    }
    *out = c;
    return S_OK;
}

void kklock_close_capture(kklock_capture *capture) { delete capture; }

int32_t kklock_grab(kklock_capture *c, void *pixels, uint32_t limit,
        uint32_t *width, uint32_t *height, uint32_t *rotation) {
    DXGI_OUTDUPL_FRAME_INFO frame{};
    ComPtr<IDXGIResource> resource;
    HRESULT hr = c->duplication->AcquireNextFrame(35, &frame, &resource);
    if (hr == DXGI_ERROR_WAIT_TIMEOUT) return S_FALSE;
    if (FAILED(hr)) return hr;
    struct FrameLease {
        IDXGIOutputDuplication *dup;
        ~FrameLease() { dup->ReleaseFrame(); }
    } lease{c->duplication.Get()};
    if (!frame.LastPresentTime.QuadPart && c->staging) return S_FALSE;
    ComPtr<ID3D11Texture2D> texture;
    if (FAILED(hr = resource.As(&texture))) return hr;
    D3D11_TEXTURE2D_DESC desc{};
    texture->GetDesc(&desc);
    if (!desc.Width || !desc.Height || desc.Width > 3840 || desc.Height > 2160 ||
            desc.Format != DXGI_FORMAT_B8G8R8A8_UNORM || uint64_t(desc.Width) * desc.Height * 4 > limit) return E_INVALIDARG;
    if (!c->staging || desc.Width != c->width || desc.Height != c->height) {
        desc.BindFlags = 0;
        desc.MiscFlags = 0;
        desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
        desc.Usage = D3D11_USAGE_STAGING;
        hr = c->device->CreateTexture2D(&desc, nullptr, c->staging.ReleaseAndGetAddressOf());
        if (FAILED(hr)) return hr;
        c->width = desc.Width;
        c->height = desc.Height;
    }
    c->context->CopyResource(c->staging.Get(), texture.Get());
    D3D11_MAPPED_SUBRESOURCE src{};
    hr = c->context->Map(c->staging.Get(), 0, D3D11_MAP_READ, 0, &src);
    if (FAILED(hr)) return hr;
    size_t row = size_t(desc.Width) * 4;
    for (UINT y = 0; y < desc.Height; y++) {
        memcpy(static_cast<uint8_t *>(pixels) + row*y, static_cast<const uint8_t *>(src.pData) + size_t(src.RowPitch)*y, row);
    }
    c->context->Unmap(c->staging.Get(), 0);
    *width = desc.Width;
    *height = desc.Height;
    *rotation = c->rotation;
    return S_OK;
}

int32_t kklock_upload(void *dev, void **tex, const void *pixels, uint32_t width, uint32_t height) {
    auto device = static_cast<ID3D11Device *>(dev);
    auto texture = static_cast<ID3D11Texture2D *>(*tex);
    if (!texture) {
        D3D11_TEXTURE2D_DESC desc{};
        desc.Width = width;
        desc.Height = height;
        desc.MipLevels = 1;
        desc.ArraySize = 1;
        desc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        desc.SampleDesc.Count = 1;
        desc.Usage = D3D11_USAGE_DEFAULT;
        HRESULT hr = device->CreateTexture2D(&desc, nullptr, &texture);
        if (FAILED(hr)) return hr;
        *tex = texture;
    }
    ComPtr<ID3D11DeviceContext> context;
    device->GetImmediateContext(&context);
    context->UpdateSubresource(texture, 0, nullptr, pixels, width*4, 0);
    return S_OK;
}
