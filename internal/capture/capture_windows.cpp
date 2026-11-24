//go:build windows && cgo

// Desktop Duplication of the primary display. See capture_windows.h. The
// Go side (capture_windows.go) drives the loop, scaling and pacing.

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <objbase.h>
#include <avrt.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <dxgi1_5.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "capture_windows.h"

namespace {

void logf(const char *fmt, ...) {
    char buf[1024];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(buf, sizeof buf, fmt, ap);
    va_end(ap);
    mmcapLog(buf);
}

template <class T> void release(T *&p) {
    if (p) {
        p->Release();
        p = nullptr;
    }
}

int64_t qpc() {
    LARGE_INTEGER t;
    QueryPerformanceCounter(&t);
    return t.QuadPart;
}

int64_t qpf() {
    static int64_t f = 0;
    if (!f) {
        LARGE_INTEGER t;
        QueryPerformanceFrequency(&t);
        f = t.QuadPart;
    }
    return f;
}

// DuplicateOutput1 requires per-monitor DPI awareness; set it on this
// thread when the API exists (Windows 10 1607+). Loaded dynamically so
// the binary still starts on systems without it.
void setThreadPerMonitorDpiAware() {
    typedef void *(WINAPI * SetCtx)(void *);
    HMODULE u = GetModuleHandleW(L"user32.dll");
    if (!u) return;
    SetCtx f = (SetCtx)(void *)GetProcAddress(u, "SetThreadDpiAwarenessContext");
    if (!f) return;
    // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 == (HANDLE)-4
    if (!f((void *)(intptr_t)-4)) logf("SetThreadDpiAwarenessContext(PER_MONITOR_AWARE_V2) failed: %lu", GetLastError());
}

} // namespace

struct mmcapDup {
    ID3D11Device *dev;
    ID3D11DeviceContext *ctx;
    IDXGIOutputDuplication *dup;
    DXGI_OUTDUPL_DESC desc;
    ID3D11Texture2D *copy;
    D3D11_TEXTURE2D_DESC copyDesc;
    int errors;
};

extern "C" {

int64_t mmcap_qpc(void) { return qpc(); }
int64_t mmcap_qpf(void) { return qpf(); }

void mmcap_thread_init(void) {
    CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    DWORD task = 0;
    if (!AvSetMmThreadCharacteristicsW(L"Capture", &task))
        logf("AvSetMmThreadCharacteristics(Capture) failed: %lu (continuing at normal priority)", GetLastError());
    setThreadPerMonitorDpiAware();
}

void mmcap_close(mmcapDup *d) {
    if (!d) return;
    release(d->copy);
    release(d->dup);
    release(d->ctx);
    release(d->dev);
    delete d;
}

int32_t mmcap_open(void *device, mmcapDup **out, mmcapInfo *info) {
    *out = nullptr;
    memset(info, 0, sizeof *info);
    ID3D11Device *dev = (ID3D11Device *)device;
    IDXGIDevice *dxgiDev = nullptr;
    IDXGIAdapter *adapter = nullptr;
    HRESULT hr = dev->QueryInterface(__uuidof(IDXGIDevice), (void **)&dxgiDev);
    if (SUCCEEDED(hr)) hr = dxgiDev->GetAdapter(&adapter);
    release(dxgiDev);
    if (FAILED(hr)) {
        logf("capture: device adapter lookup failed: hr=0x%08lX", (unsigned long)hr);
        return hr;
    }
    POINT origin = {0, 0};
    HMONITOR primary = MonitorFromPoint(origin, MONITOR_DEFAULTTOPRIMARY);
    IDXGIOutput *output = nullptr;
    for (UINT i = 0;; i++) {
        IDXGIOutput *o = nullptr;
        if (adapter->EnumOutputs(i, &o) == DXGI_ERROR_NOT_FOUND) break;
        DXGI_OUTPUT_DESC od;
        memset(&od, 0, sizeof od);
        o->GetDesc(&od);
        if (od.Monitor == primary) {
            output = o;
            break;
        }
        o->Release();
    }
    release(adapter);
    if (!output) {
        logf("capture: the primary monitor is not attached to the D3D11 device's adapter "
             "(display configuration changed?)");
        return DXGI_ERROR_NOT_FOUND;
    }

    IDXGIOutputDuplication *dup = nullptr;
    IDXGIOutput5 *o5 = nullptr;
    if (SUCCEEDED(output->QueryInterface(__uuidof(IDXGIOutput5), (void **)&o5))) {
        DXGI_FORMAT formats[] = {DXGI_FORMAT_B8G8R8A8_UNORM};
        hr = o5->DuplicateOutput1(dev, 0, 1, formats, &dup);
        o5->Release();
        if (SUCCEEDED(hr)) {
            info->duplicateOutput1 = 1;
        } else {
            logf("capture: DuplicateOutput1 failed: hr=0x%08lX; trying DuplicateOutput", (unsigned long)hr);
            dup = nullptr;
        }
    }
    if (!dup) {
        IDXGIOutput1 *o1 = nullptr;
        hr = output->QueryInterface(__uuidof(IDXGIOutput1), (void **)&o1);
        if (SUCCEEDED(hr)) {
            hr = o1->DuplicateOutput(dev, &dup);
            o1->Release();
        }
        if (FAILED(hr)) {
            const char *hint = "";
            switch (hr) {
            case E_ACCESSDENIED:
                hint = " (secure desktop: UAC prompt, lock screen or Ctrl+Alt+Del is showing)";
                break;
            case DXGI_ERROR_UNSUPPORTED:
                hint = " (Remote Desktop session, Basic Display Adapter, or the output belongs to another GPU)";
                break;
            case DXGI_ERROR_NOT_CURRENTLY_AVAILABLE:
                hint = " (too many applications are duplicating this output)";
                break;
            case DXGI_ERROR_SESSION_DISCONNECTED:
                hint = " (the session is disconnected)";
                break;
            }
            logf("capture: DuplicateOutput failed: hr=0x%08lX%s", (unsigned long)hr, hint);
            output->Release();
            return hr;
        }
    }
    output->Release();

    mmcapDup *d = new mmcapDup();
    memset(d, 0, sizeof *d);
    d->dev = dev;
    dev->AddRef();
    dev->GetImmediateContext(&d->ctx);
    d->dup = dup;
    dup->GetDesc(&d->desc);
    info->width = (int32_t)d->desc.ModeDesc.Width;
    info->height = (int32_t)d->desc.ModeDesc.Height;
    info->rotation = (int32_t)d->desc.Rotation;
    info->format = (int32_t)d->desc.ModeDesc.Format;
    info->refreshNum = (int32_t)d->desc.ModeDesc.RefreshRate.Numerator;
    info->refreshDen = (int32_t)d->desc.ModeDesc.RefreshRate.Denominator;
    info->inSystemMemory = d->desc.DesktopImageInSystemMemory ? 1 : 0;
    *out = d;
    return S_OK;
}

int32_t mmcap_acquire(mmcapDup *d, uint32_t timeoutMs, mmcapFrame *f) {
    memset(f, 0, sizeof *f);
    DXGI_OUTDUPL_FRAME_INFO fi;
    memset(&fi, 0, sizeof fi);
    IDXGIResource *res = nullptr;
    int64_t t0 = qpc();
    HRESULT hr = d->dup->AcquireNextFrame(timeoutMs, &fi, &res);
    int64_t t1 = qpc();
    f->arrivalQpc = t1;
    f->acquireMs = (float)((double)(t1 - t0) * 1000.0 / (double)qpf());
    if (hr == DXGI_ERROR_WAIT_TIMEOUT) return MMCAP_TIMEOUT;
    if (FAILED(hr)) return hr;
    f->presentQpc = fi.LastPresentTime.QuadPart;
    f->accumulated = fi.AccumulatedFrames;
    if (fi.LastPresentTime.QuadPart == 0) { // pointer-only update
        release(res);
        d->dup->ReleaseFrame();
        return MMCAP_NOIMAGE;
    }

    D3D11_TEXTURE2D_DESC want;
    memset(&want, 0, sizeof want);
    ID3D11Texture2D *tex = nullptr;
    if (!d->desc.DesktopImageInSystemMemory) {
        hr = res->QueryInterface(__uuidof(ID3D11Texture2D), (void **)&tex);
        if (FAILED(hr)) {
            logf("capture: desktop resource is not a texture: hr=0x%08lX", (unsigned long)hr);
            release(res);
            d->dup->ReleaseFrame();
            return hr;
        }
        tex->GetDesc(&want);
    } else {
        want.Width = d->desc.ModeDesc.Width;
        want.Height = d->desc.ModeDesc.Height;
        want.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
    }
    if (!d->copy || d->copyDesc.Width != want.Width || d->copyDesc.Height != want.Height ||
        d->copyDesc.Format != want.Format) {
        release(d->copy);
        D3D11_TEXTURE2D_DESC cd;
        memset(&cd, 0, sizeof cd);
        cd.Width = want.Width;
        cd.Height = want.Height;
        cd.MipLevels = 1;
        cd.ArraySize = 1;
        cd.Format = want.Format;
        cd.SampleDesc.Count = 1;
        cd.Usage = D3D11_USAGE_DEFAULT;
        cd.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
        hr = d->dev->CreateTexture2D(&cd, nullptr, &d->copy);
        if (FAILED(hr)) {
            cd.BindFlags = D3D11_BIND_RENDER_TARGET;
            hr = d->dev->CreateTexture2D(&cd, nullptr, &d->copy);
        }
        if (FAILED(hr)) {
            logf("capture: CreateTexture2D(copy %ux%u format %d) failed: hr=0x%08lX", cd.Width, cd.Height,
                 (int)cd.Format, (unsigned long)hr);
            release(tex);
            release(res);
            d->dup->ReleaseFrame();
            return hr;
        }
        d->copyDesc = cd;
        logf("capture: desktop image %ux%u format %d (system memory %d)", cd.Width, cd.Height, (int)cd.Format,
             (int)d->desc.DesktopImageInSystemMemory);
    }
    if (tex) {
        d->ctx->CopyResource(d->copy, tex);
    } else {
        DXGI_MAPPED_RECT mr;
        hr = d->dup->MapDesktopSurface(&mr);
        if (FAILED(hr)) {
            if (d->errors++ < 5) logf("capture: MapDesktopSurface failed: hr=0x%08lX", (unsigned long)hr);
            release(res);
            d->dup->ReleaseFrame();
            return hr;
        }
        d->ctx->UpdateSubresource(d->copy, 0, nullptr, mr.pBits, (UINT)mr.Pitch, 0);
        d->dup->UnMapDesktopSurface();
    }
    release(tex);
    release(res);
    d->dup->ReleaseFrame();
    f->texture = d->copy;
    f->width = (int32_t)d->copyDesc.Width;
    f->height = (int32_t)d->copyDesc.Height;
    f->format = (int32_t)d->copyDesc.Format;
    return MMCAP_FRAME;
}

} // extern "C"
