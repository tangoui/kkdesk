// Windows video view. The window is created hidden and top-level; Tango's
// NativeHost re-parents it into the session window (and answers hit tests
// with HTTRANSPARENT so input reaches the canvas). Frames arrive on the
// decoder's thread, WM_SIZE/WM_PAINT on the window's thread: both draw
// under one critical section, so a static picture is redrawn at the new
// size when the window changes without waiting for the next frame.

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <d3d11_1.h>
#include <dxgi1_2.h>
#include <dwmapi.h>
#include <math.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

#include "display_windows.h"

namespace {

const wchar_t kClass[] = L"KkdeskVideo";

void logf(const char *fmt, ...) {
    char buf[1024];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(buf, sizeof buf, fmt, ap);
    va_end(ap);
    mmdispwLog(buf);
}

template <class T> void release(T *&p) {
    if (p) {
        p->Release();
        p = nullptr;
    }
}

} // namespace

struct mmDispW {
    HWND hwnd;
    CRITICAL_SECTION cs;
    ID3D11Device *dev; // device of the current frame; everything below is on it
    ID3D11DeviceContext *ctx;
    ID3D11VideoDevice *vdev;
    ID3D11VideoContext *vctx;
    IDXGISwapChain1 *swap;
    UINT bufW, bufH;
    ID3D11Texture2D *frame; // copy of the last frame (NV12)
    UINT frameW, frameH;
    ID3D11VideoProcessorInputView *iv;
    ID3D11VideoProcessorEnumerator *en;
    ID3D11VideoProcessor *vp; // for frameW×frameH input (output size is set per blt)
    struct {
        ID3D11Texture2D *tex; // back buffer (referenced while cached)
        ID3D11VideoProcessorOutputView *view;
    } ov[2]; // output views of the swap chain's back buffers
    int ovNext;
    int presents;
    int errLogs;
    int nProcessors, nOutputViews; // created so far (tests)
};

namespace {

// releaseViews drops the cached back-buffer output views (before
// ResizeBuffers, which needs every back-buffer reference released).
void releaseViews(mmDispW *d) {
    for (auto &o : d->ov) {
        release(o.view);
        release(o.tex);
    }
}

// releaseProcessor drops the video processor and everything made from its
// enumerator.
void releaseProcessor(mmDispW *d) {
    releaseViews(d);
    release(d->iv);
    release(d->vp);
    release(d->en);
}

void resetDevice(mmDispW *d) {
    releaseProcessor(d);
    release(d->frame);
    release(d->swap);
    // Flip swap chains are destroyed lazily: flush so a new one can be
    // created for the same window right away. (No ClearState: the context
    // is shared with capture and encoding.)
    if (d->ctx) d->ctx->Flush();
    release(d->vctx);
    release(d->vdev);
    release(d->ctx);
    release(d->dev);
    d->bufW = d->bufH = d->frameW = d->frameH = 0;
}

void logHR(mmDispW *d, const char *what, HRESULT hr) {
    if (d->errLogs < 20) {
        d->errLogs++;
        logf("display: %s failed: hr=0x%08lX", what, (unsigned long)hr);
    }
}

HRESULT useDevice(mmDispW *d, ID3D11Device *dev) {
    if (d->dev == dev) return d->vctx ? S_OK : E_NOINTERFACE; // no video support: keep failing, don't crash
    if (d->dev) logf("display: frame from another D3D11 device; recreating the swap chain");
    resetDevice(d);
    d->dev = dev;
    dev->AddRef();
    dev->GetImmediateContext(&d->ctx);
    HRESULT hr = dev->QueryInterface(IID_PPV_ARGS(&d->vdev));
    if (SUCCEEDED(hr)) hr = d->ctx->QueryInterface(IID_PPV_ARGS(&d->vctx));
    if (FAILED(hr)) logHR(d, "ID3D11VideoDevice/VideoContext", hr);
    return hr;
}

void clientSize(mmDispW *d, UINT *w, UINT *h) {
    RECT r = {0, 0, 0, 0};
    GetClientRect(d->hwnd, &r);
    *w = r.right > r.left ? (UINT)(r.right - r.left) : 0;
    *h = r.bottom > r.top ? (UINT)(r.bottom - r.top) : 0;
}

// placed reports whether the window has a real size: it is created 1x1 and
// only sized once the UI toolkit has parented and placed it, so no swap
// chain is made for the temporary top-level window.
bool placed(mmDispW *d) {
    UINT w, h;
    clientSize(d, &w, &h);
    return w > 1 && h > 1;
}

HRESULT ensureSwapChain(mmDispW *d) {
    UINT w, h;
    clientSize(d, &w, &h);
    if (d->swap) {
        if (w == d->bufW && h == d->bufH) return S_OK;
        releaseViews(d);
        HRESULT hr = d->swap->ResizeBuffers(0, w, h, DXGI_FORMAT_UNKNOWN, 0);
        if (FAILED(hr)) {
            logHR(d, "ResizeBuffers", hr);
            return hr;
        }
        d->bufW = w;
        d->bufH = h;
        return S_OK;
    }
    IDXGIDevice *xd = nullptr;
    IDXGIAdapter *ad = nullptr;
    IDXGIFactory2 *f = nullptr;
    HRESULT hr = d->dev->QueryInterface(IID_PPV_ARGS(&xd));
    if (SUCCEEDED(hr)) hr = xd->GetAdapter(&ad);
    if (SUCCEEDED(hr)) hr = ad->GetParent(IID_PPV_ARGS(&f));
    if (SUCCEEDED(hr)) {
        DXGI_SWAP_CHAIN_DESC1 sd;
        memset(&sd, 0, sizeof sd);
        sd.Width = w;
        sd.Height = h;
        sd.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        sd.SampleDesc.Count = 1;
        sd.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT;
        sd.BufferCount = 2;
        sd.Scaling = DXGI_SCALING_NONE;
        sd.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;
        sd.AlphaMode = DXGI_ALPHA_MODE_IGNORE;
        hr = f->CreateSwapChainForHwnd(d->dev, d->hwnd, &sd, nullptr, nullptr, &d->swap);
        if (SUCCEEDED(hr)) {
            f->MakeWindowAssociation(d->hwnd, DXGI_MWA_NO_ALT_ENTER | DXGI_MWA_NO_WINDOW_CHANGES);
            d->bufW = w;
            d->bufH = h;
            logf("display: flip swap chain %ux%u", w, h);
        }
    }
    release(f);
    release(ad);
    release(xd);
    if (FAILED(hr)) logHR(d, "CreateSwapChainForHwnd", hr);
    return hr;
}

// ensureProcessor creates the video processor for the current frame size
// (released whenever that changes). The output size is set per blt.
HRESULT ensureProcessor(mmDispW *d) {
    if (d->vp) return S_OK;
    if (!d->vdev || !d->vctx) return E_NOINTERFACE;
    D3D11_VIDEO_PROCESSOR_CONTENT_DESC cd;
    memset(&cd, 0, sizeof cd);
    cd.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
    cd.InputFrameRate.Numerator = 60;
    cd.InputFrameRate.Denominator = 1;
    cd.InputWidth = d->frameW;
    cd.InputHeight = d->frameH;
    cd.OutputFrameRate = cd.InputFrameRate;
    cd.OutputWidth = d->frameW;
    cd.OutputHeight = d->frameH;
    cd.Usage = D3D11_VIDEO_USAGE_OPTIMAL_SPEED;
    HRESULT hr = d->vdev->CreateVideoProcessorEnumerator(&cd, &d->en);
    if (SUCCEEDED(hr)) hr = d->vdev->CreateVideoProcessor(d->en, 0, &d->vp);
    if (SUCCEEDED(hr)) {
        D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC vd;
        memset(&vd, 0, sizeof vd);
        vd.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
        hr = d->vdev->CreateVideoProcessorInputView(d->frame, d->en, &vd, &d->iv);
    }
    if (FAILED(hr)) {
        logHR(d, "creating the video processor", hr);
        releaseProcessor(d);
        return hr;
    }
    d->vctx->VideoProcessorSetStreamFrameFormat(d->vp, 0, D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE);
    d->vctx->VideoProcessorSetStreamAutoProcessingMode(d->vp, 0, FALSE);
    // Input: BT.709 limited-range YCbCr. Output: full-range RGB.
    D3D11_VIDEO_PROCESSOR_COLOR_SPACE ics;
    memset(&ics, 0, sizeof ics);
    ics.YCbCr_Matrix = 1;
    ics.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_16_235;
    D3D11_VIDEO_PROCESSOR_COLOR_SPACE ocs;
    memset(&ocs, 0, sizeof ocs);
    ocs.RGB_Range = 0;
    ocs.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_0_255;
    d->vctx->VideoProcessorSetStreamColorSpace(d->vp, 0, &ics);
    d->vctx->VideoProcessorSetOutputColorSpace(d->vp, &ocs);
    ID3D11VideoContext1 *v1 = nullptr;
    if (SUCCEEDED(d->vctx->QueryInterface(IID_PPV_ARGS(&v1)))) {
        v1->VideoProcessorSetStreamColorSpace1(d->vp, 0, DXGI_COLOR_SPACE_YCBCR_STUDIO_G22_LEFT_P709);
        v1->VideoProcessorSetOutputColorSpace1(d->vp, DXGI_COLOR_SPACE_RGB_FULL_G22_NONE_P709);
        v1->Release();
    }
    D3D11_VIDEO_COLOR black;
    memset(&black, 0, sizeof black);
    black.RGBA.A = 1;
    d->vctx->VideoProcessorSetOutputBackgroundColor(d->vp, FALSE, &black);
    d->nProcessors++;
    logf("display: video processor for NV12 %ux%u", d->frameW, d->frameH);
    return S_OK;
}

// fitRect is display.VideoRect rounded to pixels.
RECT fitRect(UINT boxW, UINT boxH, UINT fw, UINT fh) {
    double s = fmin((double)boxW / fw, (double)boxH / fh);
    double vw = fw * s, vh = fh * s;
    double x = (boxW - vw) / 2, y = (boxH - vh) / 2;
    RECT r = {(LONG)lround(x), (LONG)lround(y), (LONG)lround(x + vw), (LONG)lround(y + vh)};
    return r;
}

// outputView returns an output view of target: cached for the swap chain's
// back buffers (cache=true), a new one the caller releases otherwise.
HRESULT outputView(mmDispW *d, ID3D11Texture2D *target, bool cache, ID3D11VideoProcessorOutputView **out) {
    *out = nullptr;
    if (cache)
        for (auto &o : d->ov)
            if (o.tex == target) {
                *out = o.view;
                return S_OK;
            }
    D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC od;
    memset(&od, 0, sizeof od);
    od.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
    HRESULT hr = d->vdev->CreateVideoProcessorOutputView(target, d->en, &od, out);
    if (FAILED(hr)) {
        logHR(d, "CreateVideoProcessorOutputView", hr);
        return hr;
    }
    d->nOutputViews++;
    if (cache) {
        auto &o = d->ov[d->ovNext];
        d->ovNext = (d->ovNext + 1) % 2;
        release(o.view);
        release(o.tex);
        o.tex = target;
        target->AddRef();
        o.view = *out;
    }
    return S_OK;
}

// blt draws the current frame into target (outW x outH). d->cs held.
HRESULT blt(mmDispW *d, ID3D11Texture2D *target, UINT outW, UINT outH, bool cache) {
    HRESULT hr = ensureProcessor(d);
    if (FAILED(hr)) return hr;
    ID3D11VideoProcessorOutputView *ov = nullptr;
    hr = outputView(d, target, cache, &ov);
    if (FAILED(hr)) return hr;
    RECT src = {0, 0, (LONG)d->frameW, (LONG)d->frameH};
    RECT all = {0, 0, (LONG)outW, (LONG)outH};
    RECT dst = fitRect(outW, outH, d->frameW, d->frameH);
    d->vctx->VideoProcessorSetStreamSourceRect(d->vp, 0, TRUE, &src);
    d->vctx->VideoProcessorSetStreamDestRect(d->vp, 0, TRUE, &dst);
    d->vctx->VideoProcessorSetOutputTargetRect(d->vp, TRUE, &all);
    D3D11_VIDEO_PROCESSOR_STREAM st;
    memset(&st, 0, sizeof st);
    st.Enable = TRUE;
    st.pInputSurface = d->iv;
    hr = d->vctx->VideoProcessorBlt(d->vp, ov, 0, 1, &st);
    if (FAILED(hr)) logHR(d, "VideoProcessorBlt", hr);
    if (!cache) ov->Release();
    return hr;
}

// present draws the current frame into the back buffer and presents it.
// d->cs held.
HRESULT present(mmDispW *d) {
    if (!d->frame || !placed(d)) return S_FALSE;
    HRESULT hr = ensureSwapChain(d);
    if (FAILED(hr)) return hr;
    ID3D11Texture2D *back = nullptr;
    hr = d->swap->GetBuffer(0, IID_PPV_ARGS(&back));
    if (FAILED(hr)) {
        logHR(d, "GetBuffer", hr);
        return hr;
    }
    hr = blt(d, back, d->bufW, d->bufH, true);
    back->Release();
    if (FAILED(hr)) return hr;
    hr = d->swap->Present(0, 0);
    if (hr == DXGI_ERROR_DEVICE_REMOVED || hr == DXGI_ERROR_DEVICE_RESET) {
        logHR(d, "Present (device lost)", hr);
        resetDevice(d); // the next frame brings the new device
        return hr;
    }
    if (FAILED(hr)) {
        logHR(d, "Present", hr);
        return hr;
    }
    d->presents++;
    return S_OK;
}

const UINT_PTR kRepaintTimer = 1;

// tryRepaint redraws the current frame at the window's size unless a frame
// is being presented on another thread right now: the window's thread must
// not wait for it (DXGI may need this thread meanwhile), so the repaint is
// retried shortly from a timer instead. Returns true if it drew.
bool tryRepaint(mmDispW *d) {
    if (!TryEnterCriticalSection(&d->cs)) {
        SetTimer(d->hwnd, kRepaintTimer, 15, nullptr);
        return false;
    }
    bool drawn = present(d) == S_OK;
    LeaveCriticalSection(&d->cs);
    return drawn;
}

LRESULT CALLBACK wndProc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    mmDispW *d = (mmDispW *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
    switch (msg) {
    case WM_SIZE:
        if (d) tryRepaint(d);
        return 0;
    case WM_TIMER:
        if (wp == kRepaintTimer) {
            KillTimer(hwnd, kRepaintTimer);
            if (d) tryRepaint(d);
            return 0;
        }
        break;
    case WM_PAINT:
        if (d && (tryRepaint(d) || d->swap)) {
            ValidateRect(hwnd, nullptr); // drawn, or retried from the timer
            return 0;
        }
        break; // no frame yet: the class brush paints black
    case WM_ERASEBKGND:
        if (d && d->swap) return 1;
        break;
    }
    return DefWindowProcW(hwnd, msg, wp, lp);
}

bool registerClass() {
    static bool done;
    if (done) return true;
    WNDCLASSEXW wc;
    memset(&wc, 0, sizeof wc);
    wc.cbSize = sizeof wc;
    wc.style = CS_HREDRAW | CS_VREDRAW;
    wc.lpfnWndProc = wndProc;
    wc.hInstance = GetModuleHandleW(nullptr);
    wc.hCursor = LoadCursorW(nullptr, (LPCWSTR)IDC_ARROW);
    wc.hbrBackground = (HBRUSH)GetStockObject(BLACK_BRUSH);
    wc.lpszClassName = kClass;
    if (!RegisterClassExW(&wc) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) return false;
    done = true;
    return true;
}

} // namespace

extern "C" {

int32_t mmdispw_new(mmDispW **out, void **hwnd) {
    *out = nullptr;
    *hwnd = nullptr;
    if (!registerClass()) return HRESULT_FROM_WIN32(GetLastError());
    mmDispW *d = new mmDispW();
    memset(d, 0, sizeof *d);
    InitializeCriticalSection(&d->cs);
    d->hwnd = CreateWindowExW(0, kClass, L"kkdesk video", WS_POPUP | WS_CLIPSIBLINGS, 0, 0, 1, 1, nullptr, nullptr,
                              GetModuleHandleW(nullptr), nullptr);
    if (!d->hwnd) {
        HRESULT hr = HRESULT_FROM_WIN32(GetLastError());
        DeleteCriticalSection(&d->cs);
        delete d;
        return hr;
    }
    SetWindowLongPtrW(d->hwnd, GWLP_USERDATA, (LONG_PTR)d);
    *out = d;
    *hwnd = d->hwnd;
    return S_OK;
}

int32_t mmdispw_enqueue(mmDispW *d, void *tex, int32_t *w, int32_t *h) {
    ID3D11Texture2D *src = (ID3D11Texture2D *)tex;
    D3D11_TEXTURE2D_DESC sd;
    src->GetDesc(&sd);
    if (sd.Format != DXGI_FORMAT_NV12 || sd.Width == 0 || sd.Height == 0) return E_INVALIDARG;
    *w = (int32_t)sd.Width;
    *h = (int32_t)sd.Height;
    ID3D11Device *dev = nullptr;
    src->GetDevice(&dev);
    EnterCriticalSection(&d->cs);
    HRESULT hr = useDevice(d, dev);
    if (SUCCEEDED(hr) && (!d->frame || d->frameW != sd.Width || d->frameH != sd.Height)) {
        releaseProcessor(d);
        release(d->frame);
        D3D11_TEXTURE2D_DESC fd;
        memset(&fd, 0, sizeof fd);
        fd.Width = sd.Width;
        fd.Height = sd.Height;
        fd.MipLevels = 1;
        fd.ArraySize = 1;
        fd.Format = DXGI_FORMAT_NV12;
        fd.SampleDesc.Count = 1;
        fd.Usage = D3D11_USAGE_DEFAULT;
        fd.BindFlags = 0; // copy target and video processor input only: no NV12 render target (optional on some GPUs)
        hr = d->dev->CreateTexture2D(&fd, nullptr, &d->frame);
        if (SUCCEEDED(hr)) {
            d->frameW = sd.Width;
            d->frameH = sd.Height;
        } else {
            logHR(d, "CreateTexture2D(NV12 frame)", hr);
        }
    }
    if (SUCCEEDED(hr)) {
        d->ctx->CopySubresourceRegion(d->frame, 0, 0, 0, 0, src, D3D11CalcSubresource(0, 0, sd.MipLevels), nullptr);
        hr = present(d);
    }
    LeaveCriticalSection(&d->cs);
    dev->Release();
    return hr;
}

int32_t mmdispw_render_to(mmDispW *d, void *target) {
    ID3D11Texture2D *t = (ID3D11Texture2D *)target;
    D3D11_TEXTURE2D_DESC td;
    t->GetDesc(&td);
    EnterCriticalSection(&d->cs);
    HRESULT hr = d->frame ? blt(d, t, td.Width, td.Height, false) : E_UNEXPECTED;
    LeaveCriticalSection(&d->cs);
    return hr;
}

void mmdispw_hold(mmDispW *d, int32_t on) {
    if (on)
        EnterCriticalSection(&d->cs);
    else
        LeaveCriticalSection(&d->cs);
}

void mmdispw_counts(mmDispW *d, int32_t *processors, int32_t *outputViews) {
    EnterCriticalSection(&d->cs);
    *processors = d->nProcessors;
    *outputViews = d->nOutputViews;
    LeaveCriticalSection(&d->cs);
}

int32_t mmdispw_presents(mmDispW *d) {
    EnterCriticalSection(&d->cs);
    int n = d->presents;
    LeaveCriticalSection(&d->cs);
    return n;
}

int32_t mmdispw_vsync(int64_t *vblank, int64_t *period, int64_t *now, int64_t *freq) {
    DWM_TIMING_INFO ti;
    memset(&ti, 0, sizeof ti);
    ti.cbSize = sizeof ti;
    HRESULT hr = DwmGetCompositionTimingInfo(nullptr, &ti);
    LARGE_INTEGER n, f;
    QueryPerformanceCounter(&n);
    QueryPerformanceFrequency(&f);
    *vblank = (int64_t)ti.qpcVBlank;
    *period = (int64_t)ti.qpcRefreshPeriod;
    *now = n.QuadPart;
    *freq = f.QuadPart;
    return hr;
}

void mmdispw_destroy(mmDispW *d) {
    if (!d) return;
    EnterCriticalSection(&d->cs);
    SetWindowLongPtrW(d->hwnd, GWLP_USERDATA, 0);
    KillTimer(d->hwnd, kRepaintTimer);
    resetDevice(d);
    LeaveCriticalSection(&d->cs);
    DestroyWindow(d->hwnd);
    DeleteCriticalSection(&d->cs);
    delete d;
}

} // extern "C"
