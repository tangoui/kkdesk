//go:build windows && cgo

// Shared D3D11 device and video processor for the Windows backends. See
// winmedia_windows.h.

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <objbase.h>
#include <d3d11_4.h>
#include <d3d10.h>
#include <dxgi1_2.h>
#include <mfapi.h>
#include <mfidl.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "winmedia_windows.h"

namespace {

void logf(const char *fmt, ...) {
    char buf[1024];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(buf, sizeof buf, fmt, ap);
    va_end(ap);
    mmwmLog(buf);
}

template <class T> void release(T *&p) {
    if (p) {
        p->Release();
        p = nullptr;
    }
}

void narrow(const WCHAR *w, char *out, int n) {
    if (WideCharToMultiByte(CP_UTF8, 0, w, -1, out, n, nullptr, nullptr) <= 0 && n > 0) out[0] = 0;
}

const char *rotationName(DXGI_MODE_ROTATION r) {
    switch (r) {
    case DXGI_MODE_ROTATION_IDENTITY: return "identity";
    case DXGI_MODE_ROTATION_ROTATE90: return "90";
    case DXGI_MODE_ROTATION_ROTATE180: return "180";
    case DXGI_MODE_ROTATION_ROTATE270: return "270";
    default: return "unspecified";
    }
}

// Logs every adapter and output; returns (AddRef'd) the adapter owning the
// primary monitor, or nullptr.
IDXGIAdapter1 *findPrimaryAdapter() {
    IDXGIFactory1 *factory = nullptr;
    HRESULT hr = CreateDXGIFactory1(__uuidof(IDXGIFactory1), (void **)&factory);
    if (FAILED(hr)) {
        logf("CreateDXGIFactory1 failed: hr=0x%08lX", (unsigned long)hr);
        return nullptr;
    }
    POINT origin = {0, 0};
    HMONITOR primary = MonitorFromPoint(origin, MONITOR_DEFAULTTOPRIMARY);
    IDXGIAdapter1 *found = nullptr;
    for (UINT i = 0;; i++) {
        IDXGIAdapter1 *adapter = nullptr;
        if (factory->EnumAdapters1(i, &adapter) == DXGI_ERROR_NOT_FOUND) break;
        DXGI_ADAPTER_DESC1 ad;
        memset(&ad, 0, sizeof ad);
        adapter->GetDesc1(&ad);
        char name[256];
        narrow(ad.Description, name, sizeof name);
        LARGE_INTEGER umd;
        umd.QuadPart = 0;
        char driver[64] = "unknown";
        if (SUCCEEDED(adapter->CheckInterfaceSupport(__uuidof(IDXGIDevice), &umd))) {
            snprintf(driver, sizeof driver, "%u.%u.%u.%u", (unsigned)HIWORD(umd.HighPart),
                     (unsigned)LOWORD(umd.HighPart), (unsigned)HIWORD(umd.LowPart),
                     (unsigned)LOWORD(umd.LowPart));
        }
        logf("adapter %u: %s vendor=0x%04X device=0x%04X driver=%s vram=%lluMB flags=0x%X luid=%08lX:%08lX",
             i, name, ad.VendorId, ad.DeviceId, driver,
             (unsigned long long)(ad.DedicatedVideoMemory >> 20), ad.Flags,
             (unsigned long)ad.AdapterLuid.HighPart, (unsigned long)ad.AdapterLuid.LowPart);
        for (UINT j = 0;; j++) {
            IDXGIOutput *out = nullptr;
            if (adapter->EnumOutputs(j, &out) == DXGI_ERROR_NOT_FOUND) break;
            DXGI_OUTPUT_DESC od;
            memset(&od, 0, sizeof od);
            out->GetDesc(&od);
            char dn[64];
            narrow(od.DeviceName, dn, sizeof dn);
            bool isPrimary = od.Monitor == primary;
            logf("  output %u: %s desktop=(%ld,%ld)-(%ld,%ld) attached=%d rotation=%s%s", j, dn,
                 od.DesktopCoordinates.left, od.DesktopCoordinates.top, od.DesktopCoordinates.right,
                 od.DesktopCoordinates.bottom, (int)od.AttachedToDesktop, rotationName(od.Rotation),
                 isPrimary ? " PRIMARY" : "");
            if (isPrimary && !found) {
                found = adapter;
                found->AddRef();
            }
            out->Release();
        }
        adapter->Release();
    }
    factory->Release();
    if (!found) logf("no adapter owns the primary monitor; using the default adapter");
    return found;
}

D3D11_VIDEO_PROCESSOR_ROTATION vpRotation(int dxgiRotation) {
    // The duplicated image is in the panel's native orientation; rotate it
    // back to what the user sees. Direction not verified on hardware yet.
    switch (dxgiRotation) {
    case DXGI_MODE_ROTATION_ROTATE90: return D3D11_VIDEO_PROCESSOR_ROTATION_270;
    case DXGI_MODE_ROTATION_ROTATE180: return D3D11_VIDEO_PROCESSOR_ROTATION_180;
    case DXGI_MODE_ROTATION_ROTATE270: return D3D11_VIDEO_PROCESSOR_ROTATION_90;
    default: return D3D11_VIDEO_PROCESSOR_ROTATION_IDENTITY;
    }
}

} // namespace

struct mmwmConv {
    ID3D11Device *dev;
    ID3D11DeviceContext *ctx;
    ID3D11VideoDevice *vdev;
    ID3D11VideoContext *vctx;
    ID3D11VideoProcessorEnumerator *en;
    ID3D11VideoProcessor *vp;
    struct In {
        ID3D11Texture2D *tex;
        ID3D11VideoProcessorInputView *view;
    } in[4];
    struct Out {
        ID3D11Texture2D *tex;
        ID3D11VideoProcessorOutputView *view;
    } out[8];
    int nextIn, nextOut;
    int bltErrors;
};

extern "C" {

void mmwm_com_init(void) { CoInitializeEx(nullptr, COINIT_MULTITHREADED); }

void mmwm_release(void *unk) {
    if (unk) ((IUnknown *)unk)->Release();
}

int32_t mmwm_create_device(void **device, void **manager, uint64_t *luid) {
    *device = nullptr;
    *manager = nullptr;
    *luid = 0;
    mmwm_com_init();

    IDXGIAdapter1 *adapter = findPrimaryAdapter();
    static const D3D_FEATURE_LEVEL levels[] = {D3D_FEATURE_LEVEL_11_1, D3D_FEATURE_LEVEL_11_0,
                                               D3D_FEATURE_LEVEL_10_1, D3D_FEATURE_LEVEL_10_0};
    UINT flags = D3D11_CREATE_DEVICE_VIDEO_SUPPORT | D3D11_CREATE_DEVICE_BGRA_SUPPORT;
    ID3D11Device *dev = nullptr;
    D3D_FEATURE_LEVEL got = (D3D_FEATURE_LEVEL)0;
    D3D_DRIVER_TYPE type = adapter ? D3D_DRIVER_TYPE_UNKNOWN : D3D_DRIVER_TYPE_HARDWARE;
    HRESULT hr = D3D11CreateDevice(adapter, type, nullptr, flags, levels, 4, D3D11_SDK_VERSION, &dev,
                                   &got, nullptr);
    if (hr == E_INVALIDARG) { // runtimes without 11_1 reject the array
        hr = D3D11CreateDevice(adapter, type, nullptr, flags, levels + 1, 3, D3D11_SDK_VERSION, &dev,
                               &got, nullptr);
    }
    if (FAILED(hr)) {
        logf("D3D11CreateDevice(VIDEO_SUPPORT|BGRA_SUPPORT) failed: hr=0x%08lX; retrying without VIDEO_SUPPORT "
             "(the video processor will then be unavailable)",
             (unsigned long)hr);
        flags = D3D11_CREATE_DEVICE_BGRA_SUPPORT;
        hr = D3D11CreateDevice(adapter, type, nullptr, flags, levels + 1, 3, D3D11_SDK_VERSION, &dev, &got,
                               nullptr);
    }
    if (FAILED(hr)) {
        logf("D3D11CreateDevice failed: hr=0x%08lX", (unsigned long)hr);
        release(adapter);
        return hr;
    }
    {
        IDXGIDevice *dxgiDev = nullptr;
        IDXGIAdapter *a = nullptr;
        if (SUCCEEDED(dev->QueryInterface(__uuidof(IDXGIDevice), (void **)&dxgiDev)) &&
            SUCCEEDED(dxgiDev->GetAdapter(&a))) {
            DXGI_ADAPTER_DESC ad;
            memset(&ad, 0, sizeof ad);
            a->GetDesc(&ad);
            char name[256];
            narrow(ad.Description, name, sizeof name);
            *luid = ((uint64_t)(uint32_t)ad.AdapterLuid.HighPart << 32) | (uint64_t)ad.AdapterLuid.LowPart;
            logf("D3D11 device on \"%s\" feature level 0x%X flags 0x%X", name, (unsigned)got, flags);
        }
        release(a);
        release(dxgiDev);
    }
    release(adapter);

    // The device is shared by the capture thread, the encoder thread and
    // the MFT's own threads.
    // ID3D10Multithread on the device is the long-standing way (used by
    // Media Foundation samples); ID3D11Multithread lives on the context.
    {
        bool ok = false;
        ID3D10Multithread *mt10 = nullptr;
        if (SUCCEEDED(dev->QueryInterface(__uuidof(ID3D10Multithread), (void **)&mt10))) {
            mt10->SetMultithreadProtected(TRUE);
            mt10->Release();
            ok = true;
        }
        ID3D11DeviceContext *ctx = nullptr;
        dev->GetImmediateContext(&ctx);
        ID3D11Multithread *mt = nullptr;
        if (ctx && SUCCEEDED(ctx->QueryInterface(__uuidof(ID3D11Multithread), (void **)&mt))) {
            mt->SetMultithreadProtected(TRUE);
            mt->Release();
            ok = true;
        }
        release(ctx);
        if (!ok) logf("warning: could not enable D3D11 multithread protection");
    }

    hr = MFStartup(MF_VERSION, MFSTARTUP_FULL);
    if (FAILED(hr)) {
        logf("MFStartup failed: hr=0x%08lX (Media Foundation missing? N editions need the Media Feature Pack)",
             (unsigned long)hr);
        dev->Release();
        return hr;
    }
    UINT token = 0;
    IMFDXGIDeviceManager *mgr = nullptr;
    hr = MFCreateDXGIDeviceManager(&token, &mgr);
    if (SUCCEEDED(hr)) hr = mgr->ResetDevice(dev, token);
    if (FAILED(hr)) {
        logf("IMFDXGIDeviceManager setup failed: hr=0x%08lX", (unsigned long)hr);
        release(mgr);
        dev->Release();
        return hr;
    }
    *device = dev;
    *manager = mgr;
    return S_OK;
}

int32_t mmwm_device_removed_reason(void *device) {
    if (!device) return E_POINTER;
    return ((ID3D11Device *)device)->GetDeviceRemovedReason();
}

int32_t mmwm_new_nv12(void *device, int w, int h, void **texture) {
    *texture = nullptr;
    D3D11_TEXTURE2D_DESC d;
    memset(&d, 0, sizeof d);
    d.Width = (UINT)w;
    d.Height = (UINT)h;
    d.MipLevels = 1;
    d.ArraySize = 1;
    d.Format = DXGI_FORMAT_NV12;
    d.SampleDesc.Count = 1;
    d.Usage = D3D11_USAGE_DEFAULT;
    d.BindFlags = D3D11_BIND_RENDER_TARGET;
    ID3D11Texture2D *t = nullptr;
    HRESULT hr = ((ID3D11Device *)device)->CreateTexture2D(&d, nullptr, &t);
    if (FAILED(hr)) {
        logf("CreateTexture2D(NV12 %dx%d) failed: hr=0x%08lX", w, h, (unsigned long)hr);
        return hr;
    }
    *texture = t;
    return S_OK;
}

void mmwm_conv_destroy(mmwmConv *c) {
    if (!c) return;
    for (auto &v : c->in) {
        release(v.view);
        release(v.tex);
    }
    for (auto &v : c->out) {
        release(v.view);
        release(v.tex);
    }
    release(c->vp);
    release(c->en);
    release(c->vctx);
    release(c->vdev);
    release(c->ctx);
    release(c->dev);
    delete c;
}

int32_t mmwm_conv_create(void *device, int inW, int inH, int inFormat, int outW, int outH, int rotation,
                         mmwmConv **out) {
    *out = nullptr;
    mmwm_com_init();
    mmwmConv *c = new mmwmConv();
    memset(c, 0, sizeof *c);
    c->dev = (ID3D11Device *)device;
    c->dev->AddRef();
    c->dev->GetImmediateContext(&c->ctx);
    HRESULT hr = c->dev->QueryInterface(__uuidof(ID3D11VideoDevice), (void **)&c->vdev);
    if (FAILED(hr)) {
        logf("video processor: device has no ID3D11VideoDevice (created without VIDEO_SUPPORT?): hr=0x%08lX",
             (unsigned long)hr);
        mmwm_conv_destroy(c);
        return hr;
    }
    hr = c->ctx->QueryInterface(__uuidof(ID3D11VideoContext), (void **)&c->vctx);
    if (FAILED(hr)) {
        logf("video processor: no ID3D11VideoContext: hr=0x%08lX", (unsigned long)hr);
        mmwm_conv_destroy(c);
        return hr;
    }
    D3D11_VIDEO_PROCESSOR_CONTENT_DESC cd;
    memset(&cd, 0, sizeof cd);
    cd.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
    cd.InputFrameRate.Numerator = 60;
    cd.InputFrameRate.Denominator = 1;
    cd.InputWidth = (UINT)inW;
    cd.InputHeight = (UINT)inH;
    cd.OutputFrameRate = cd.InputFrameRate;
    cd.OutputWidth = (UINT)outW;
    cd.OutputHeight = (UINT)outH;
    cd.Usage = D3D11_VIDEO_USAGE_OPTIMAL_SPEED;
    hr = c->vdev->CreateVideoProcessorEnumerator(&cd, &c->en);
    if (FAILED(hr)) {
        logf("CreateVideoProcessorEnumerator(%dx%d -> %dx%d) failed: hr=0x%08lX", inW, inH, outW, outH,
             (unsigned long)hr);
        mmwm_conv_destroy(c);
        return hr;
    }
    UINT fin = 0, fout = 0;
    c->en->CheckVideoProcessorFormat((DXGI_FORMAT)inFormat, &fin);
    c->en->CheckVideoProcessorFormat(DXGI_FORMAT_NV12, &fout);
    if (!(fin & D3D11_VIDEO_PROCESSOR_FORMAT_SUPPORT_INPUT) || !(fout & D3D11_VIDEO_PROCESSOR_FORMAT_SUPPORT_OUTPUT)) {
        logf("video processor does not support format %d -> NV12 (input caps 0x%X, NV12 caps 0x%X)", inFormat, fin,
             fout);
        mmwm_conv_destroy(c);
        return E_NOTIMPL;
    }
    D3D11_VIDEO_PROCESSOR_CAPS caps;
    memset(&caps, 0, sizeof caps);
    c->en->GetVideoProcessorCaps(&caps);
    hr = c->vdev->CreateVideoProcessor(c->en, 0, &c->vp);
    if (FAILED(hr)) {
        logf("CreateVideoProcessor failed: hr=0x%08lX", (unsigned long)hr);
        mmwm_conv_destroy(c);
        return hr;
    }
    RECT src = {0, 0, inW, inH};
    RECT dst = {0, 0, outW, outH};
    c->vctx->VideoProcessorSetStreamFrameFormat(c->vp, 0, D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE);
    c->vctx->VideoProcessorSetStreamSourceRect(c->vp, 0, TRUE, &src);
    c->vctx->VideoProcessorSetStreamDestRect(c->vp, 0, TRUE, &dst);
    c->vctx->VideoProcessorSetOutputTargetRect(c->vp, TRUE, &dst);
    c->vctx->VideoProcessorSetStreamAutoProcessingMode(c->vp, 0, FALSE);
    D3D11_VIDEO_PROCESSOR_ROTATION rot = vpRotation(rotation);
    if (rot != D3D11_VIDEO_PROCESSOR_ROTATION_IDENTITY) {
        if (caps.FeatureCaps & D3D11_VIDEO_PROCESSOR_FEATURE_CAPS_ROTATION) {
            c->vctx->VideoProcessorSetStreamRotation(c->vp, 0, TRUE, rot);
            logf("video processor: display rotation %d -> stream rotation %d (direction unverified)", rotation,
                 (int)rot);
        } else {
            logf("warning: display is rotated (%d) but the video processor cannot rotate; image will be sideways",
                 rotation);
        }
    }
    // Input: full-range RGB. Output: BT.709 limited-range YCbCr.
    D3D11_VIDEO_PROCESSOR_COLOR_SPACE ics;
    memset(&ics, 0, sizeof ics);
    ics.RGB_Range = 0;
    ics.YCbCr_Matrix = 1;
    ics.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_0_255;
    D3D11_VIDEO_PROCESSOR_COLOR_SPACE ocs;
    memset(&ocs, 0, sizeof ocs);
    ocs.RGB_Range = 1;
    ocs.YCbCr_Matrix = 1;
    ocs.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_16_235;
    c->vctx->VideoProcessorSetStreamColorSpace(c->vp, 0, &ics);
    c->vctx->VideoProcessorSetOutputColorSpace(c->vp, &ocs);
    ID3D11VideoContext1 *vctx1 = nullptr;
    if (SUCCEEDED(c->vctx->QueryInterface(__uuidof(ID3D11VideoContext1), (void **)&vctx1))) {
        vctx1->VideoProcessorSetStreamColorSpace1(c->vp, 0, DXGI_COLOR_SPACE_RGB_FULL_G22_NONE_P709);
        vctx1->VideoProcessorSetOutputColorSpace1(c->vp, DXGI_COLOR_SPACE_YCBCR_STUDIO_G22_LEFT_P709);
        vctx1->Release();
    }
    logf("video processor: format %d %dx%d -> NV12 %dx%d (BT.709 limited), feature caps 0x%X", inFormat, inW, inH,
         outW, outH, caps.FeatureCaps);
    *out = c;
    return S_OK;
}

int32_t mmwm_conv_blt(mmwmConv *c, void *srcTexture, void *dstTexture) {
    if (!c || !srcTexture || !dstTexture) return E_POINTER;
    ID3D11Texture2D *src = (ID3D11Texture2D *)srcTexture;
    ID3D11Texture2D *dst = (ID3D11Texture2D *)dstTexture;
    ID3D11VideoProcessorInputView *iv = nullptr;
    for (auto &v : c->in)
        if (v.tex == src) iv = v.view;
    HRESULT hr;
    if (!iv) {
        D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC d;
        memset(&d, 0, sizeof d);
        d.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
        hr = c->vdev->CreateVideoProcessorInputView(src, c->en, &d, &iv);
        if (FAILED(hr)) {
            logf("CreateVideoProcessorInputView failed: hr=0x%08lX", (unsigned long)hr);
            return hr;
        }
        auto &slot = c->in[c->nextIn];
        c->nextIn = (c->nextIn + 1) % 4;
        release(slot.view);
        release(slot.tex);
        slot.tex = src;
        src->AddRef(); // keep the pointer from being reused while cached
        slot.view = iv;
    }
    ID3D11VideoProcessorOutputView *ov = nullptr;
    for (auto &v : c->out)
        if (v.tex == dst) ov = v.view;
    if (!ov) {
        D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC d;
        memset(&d, 0, sizeof d);
        d.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
        hr = c->vdev->CreateVideoProcessorOutputView(dst, c->en, &d, &ov);
        if (FAILED(hr)) {
            logf("CreateVideoProcessorOutputView failed: hr=0x%08lX", (unsigned long)hr);
            return hr;
        }
        auto &slot = c->out[c->nextOut];
        c->nextOut = (c->nextOut + 1) % 8;
        release(slot.view);
        release(slot.tex);
        slot.tex = dst;
        dst->AddRef();
        slot.view = ov;
    }
    D3D11_VIDEO_PROCESSOR_STREAM st;
    memset(&st, 0, sizeof st);
    st.Enable = TRUE;
    st.pInputSurface = iv;
    hr = c->vctx->VideoProcessorBlt(c->vp, ov, 0, 1, &st);
    if (FAILED(hr) && c->bltErrors++ < 5) logf("VideoProcessorBlt failed: hr=0x%08lX", (unsigned long)hr);
    return hr;
}

} // extern "C"
