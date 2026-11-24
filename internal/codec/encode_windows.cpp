//go:build windows && cgo

// Media Foundation H.264 encoder. See encode_windows.h. The Go side
// (encode_windows.go) drives the event loop and the output conversion.

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <objbase.h>
#include <d3d11.h>
#include <d3d10.h>
#include <mfapi.h>
#include <mferror.h>
#include <mfidl.h>
#include <mftransform.h>
#include <codecapi.h>
#include <icodecapi.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "encode_windows.h"

// MFTEnum2 (Windows 10 1703+) is not declared by mingw-w64; it is looked
// up at run time so a missing export only disables adapter filtering.
typedef HRESULT(WINAPI *MFTEnum2Fn)(GUID guidCategory, UINT32 Flags, const MFT_REGISTER_TYPE_INFO *pInputType,
                                    const MFT_REGISTER_TYPE_INFO *pOutputType, IMFAttributes *pAttributes,
                                    IMFActivate ***pppMFTActivate, UINT32 *pnumMFTActivate);

namespace {

// {1D39518C-E220-4DA8-A07F-BA172552D6B1}
const GUID kMFT_ENUM_ADAPTER_LUID = {0x1d39518c, 0xe220, 0x4da8, {0xa0, 0x7f, 0xba, 0x17, 0x25, 0x52, 0xd6, 0xb1}};
// {6CA50344-051A-4DED-9779-A43305165E35}
const CLSID kCLSID_CMSH264EncoderMFT = {0x6ca50344, 0x051a, 0x4ded, {0x97, 0x79, 0xa4, 0x33, 0x05, 0x16, 0x5e, 0x35}};

const int kPoolSize = 8;

void logf(const char *fmt, ...) {
    char buf[1024];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(buf, sizeof buf, fmt, ap);
    va_end(ap);
    mmencLog(buf);
}

template <class T> void release(T *&p) {
    if (p) {
        p->Release();
        p = nullptr;
    }
}

HRESULT setU32(ICodecAPI *api, const GUID &g, UINT32 v) {
    if (!api) return E_NOINTERFACE;
    VARIANT var;
    memset(&var, 0, sizeof var);
    var.vt = VT_UI4;
    var.ulVal = v;
    return api->SetValue(&g, &var);
}

HRESULT setBool(ICodecAPI *api, const GUID &g, bool v) {
    if (!api) return E_NOINTERFACE;
    VARIANT var;
    memset(&var, 0, sizeof var);
    var.vt = VT_BOOL;
    var.boolVal = v ? VARIANT_TRUE : VARIANT_FALSE;
    return api->SetValue(&g, &var);
}

void setColor(IMFMediaType *t) {
    t->SetUINT32(MF_MT_VIDEO_PRIMARIES, MFVideoPrimaries_BT709);
    t->SetUINT32(MF_MT_TRANSFER_FUNCTION, MFVideoTransFunc_709);
    t->SetUINT32(MF_MT_YUV_MATRIX, MFVideoTransferMatrix_BT709);
    t->SetUINT32(MF_MT_VIDEO_NOMINAL_RANGE, MFNominalRange_16_235);
}

void setVideo(IMFMediaType *t, const mmencConfig *c) {
    MFSetAttributeSize(t, MF_MT_FRAME_SIZE, (UINT32)c->width, (UINT32)c->height);
    MFSetAttributeRatio(t, MF_MT_FRAME_RATE, (UINT32)c->fps, 1);
    MFSetAttributeRatio(t, MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
    t->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
    setColor(t);
}

} // namespace

struct mmEncW {
    IMFActivate *activate;
    IMFTransform *mft;
    IMFMediaEventGenerator *gen;
    IMFShutdown *shutdown;
    ICodecAPI *api;
    ID3D11Device *dev;
    ID3D11DeviceContext *ctx;
    ID3D10Multithread *mt;
    bool async, hw, providesSamples;
    DWORD inId, outId;
    DWORD outSize;
    mmencConfig cfg;

    ID3D11Texture2D *pool[kPoolSize];
    IMFSample *poolSample[kPoolSize];
    int poolNext;
    int poolBindIdx;
    bool poolLogged;

    ID3D11Texture2D *staging;
    IMFSample *outSample;

    uint8_t *buf;
    size_t bufCap;
    uint8_t *hdr;
    UINT32 hdrLen;
    int errLogs;
};

namespace {

void applyCodecAPI(mmEncW *e, bool logFailures) {
    if (!e->api) {
        if (logFailures) logf("encoder: no ICodecAPI; using default rate control and latency");
        return;
    }
    struct {
        const char *name;
        HRESULT hr;
    } r[] = {
        {"AVLowLatencyMode", setBool(e->api, CODECAPI_AVLowLatencyMode, true)},
        {"AVEncCommonRateControlMode=CBR",
         setU32(e->api, CODECAPI_AVEncCommonRateControlMode, eAVEncCommonRateControlMode_CBR)},
        {"AVEncCommonMeanBitRate", setU32(e->api, CODECAPI_AVEncCommonMeanBitRate, (UINT32)e->cfg.bps)},
        {"AVEncMPVGOPSize", setU32(e->api, CODECAPI_AVEncMPVGOPSize, (UINT32)(e->cfg.fps * 2))},
        {"AVEncMPVDefaultBPictureCount=0", setU32(e->api, CODECAPI_AVEncMPVDefaultBPictureCount, 0)},
    };
    for (auto &x : r)
        if (FAILED(x.hr) && logFailures) logf("encoder: ICodecAPI %s failed: hr=0x%08lX", x.name, (unsigned long)x.hr);
}

HRESULT setOutputType(mmEncW *e) {
    static const UINT32 profiles[] = {eAVEncH264VProfile_Base, eAVEncH264VProfile_Main, eAVEncH264VProfile_High};
    static const char *names[] = {"Baseline", "Main", "High"};
    HRESULT hr = E_FAIL;
    for (int i = 0; i < 3; i++) {
        IMFMediaType *t = nullptr;
        hr = MFCreateMediaType(&t);
        if (FAILED(hr)) return hr;
        t->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
        t->SetGUID(MF_MT_SUBTYPE, MFVideoFormat_H264);
        t->SetUINT32(MF_MT_AVG_BITRATE, (UINT32)e->cfg.bps);
        setVideo(t, &e->cfg);
        t->SetUINT32(MF_MT_MPEG2_PROFILE, profiles[i]);
        hr = e->mft->SetOutputType(e->outId, t, 0);
        t->Release();
        if (SUCCEEDED(hr)) {
            logf("encoder: output type H.264 %s %dx%d @ %d fps, %d bps", names[i], e->cfg.width, e->cfg.height,
                 e->cfg.fps, e->cfg.bps);
            return hr;
        }
        logf("encoder: SetOutputType(H.264 %s) failed: hr=0x%08lX", names[i], (unsigned long)hr);
    }
    return hr;
}

HRESULT setInputType(mmEncW *e) {
    HRESULT hr;
    IMFMediaType *t = nullptr;
    // Prefer the MFT's own NV12 type (it may carry vendor attributes).
    for (DWORD i = 0;; i++) {
        IMFMediaType *a = nullptr;
        if (FAILED(e->mft->GetInputAvailableType(e->inId, i, &a))) break;
        GUID sub = GUID_NULL;
        a->GetGUID(MF_MT_SUBTYPE, &sub);
        if (sub == MFVideoFormat_NV12) {
            t = a;
            break;
        }
        a->Release();
    }
    if (!t) {
        hr = MFCreateMediaType(&t);
        if (FAILED(hr)) return hr;
        t->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
        t->SetGUID(MF_MT_SUBTYPE, MFVideoFormat_NV12);
    }
    setVideo(t, &e->cfg);
    if (!e->hw) t->SetUINT32(MF_MT_DEFAULT_STRIDE, (UINT32)e->cfg.width);
    hr = e->mft->SetInputType(e->inId, t, 0);
    t->Release();
    if (FAILED(hr)) logf("encoder: SetInputType(NV12 %dx%d) failed: hr=0x%08lX", e->cfg.width, e->cfg.height,
                         (unsigned long)hr);
    return hr;
}

void captureSeqHeader(mmEncW *e) {
    IMFMediaType *t = nullptr;
    if (FAILED(e->mft->GetOutputCurrentType(e->outId, &t))) return;
    UINT32 n = 0;
    if (SUCCEEDED(t->GetBlobSize(MF_MT_MPEG_SEQUENCE_HEADER, &n)) && n > 0) {
        uint8_t *b = (uint8_t *)realloc(e->hdr, n);
        if (b) {
            e->hdr = b;
            if (SUCCEEDED(t->GetBlob(MF_MT_MPEG_SEQUENCE_HEADER, b, n, &n)))
                e->hdrLen = n;
        }
    }
    t->Release();
}

// Common configuration once e->mft is set.
HRESULT configure(mmEncW *e, IMFDXGIDeviceManager *mgr) {
    HRESULT hr;
    IMFAttributes *attrs = nullptr;
    if (SUCCEEDED(e->mft->GetAttributes(&attrs)) && attrs) {
        UINT32 async = 0;
        attrs->GetUINT32(MF_TRANSFORM_ASYNC, &async);
        if (async) {
            hr = attrs->SetUINT32(MF_TRANSFORM_ASYNC_UNLOCK, TRUE);
            if (FAILED(hr)) logf("encoder: MF_TRANSFORM_ASYNC_UNLOCK failed: hr=0x%08lX", (unsigned long)hr);
            e->async = true;
        }
        UINT32 d3d11 = 0;
        attrs->GetUINT32(MF_SA_D3D11_AWARE, &d3d11);
        logf("encoder: MFT async=%d d3d11-aware=%u", (int)e->async, d3d11);
        attrs->Release();
    }
    if (e->async) {
        hr = e->mft->QueryInterface(IID_PPV_ARGS(&e->gen));
        if (FAILED(hr)) {
            logf("encoder: async MFT without IMFMediaEventGenerator: hr=0x%08lX", (unsigned long)hr);
            return hr;
        }
        if (FAILED(e->mft->QueryInterface(IID_PPV_ARGS(&e->shutdown))))
            logf("encoder: async MFT without IMFShutdown; events will be polled");
    }
    DWORD nin = 0, nout = 0;
    if (SUCCEEDED(e->mft->GetStreamCount(&nin, &nout)) && nin == 1 && nout == 1) {
        DWORD in = 0, out = 0;
        if (SUCCEEDED(e->mft->GetStreamIDs(1, &in, 1, &out))) {
            e->inId = in;
            e->outId = out;
        }
    }
    e->mft->QueryInterface(IID_PPV_ARGS(&e->api));
    if (mgr) {
        hr = e->mft->ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)mgr);
        if (FAILED(hr)) {
            logf("encoder: MFT_MESSAGE_SET_D3D_MANAGER failed: hr=0x%08lX", (unsigned long)hr);
            return hr;
        }
    }
    applyCodecAPI(e, false); // some encoders need these before the media types
    hr = setOutputType(e);
    if (FAILED(hr)) return hr;
    hr = setInputType(e);
    if (FAILED(hr)) return hr;
    applyCodecAPI(e, true); // failures that remain after the types are set are logged
    MFT_OUTPUT_STREAM_INFO si;
    memset(&si, 0, sizeof si);
    if (SUCCEEDED(e->mft->GetOutputStreamInfo(e->outId, &si))) {
        e->providesSamples =
            (si.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES | MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES)) != 0;
        e->outSize = si.cbSize;
    }
    if (e->outSize < (DWORD)(e->cfg.width * e->cfg.height * 3 / 2)) e->outSize = e->cfg.width * e->cfg.height * 3 / 2;
    captureSeqHeader(e);
    hr = e->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0);
    if (FAILED(hr)) {
        logf("encoder: NOTIFY_BEGIN_STREAMING failed: hr=0x%08lX", (unsigned long)hr);
        return hr;
    }
    hr = e->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
    if (FAILED(hr)) {
        logf("encoder: NOTIFY_START_OF_STREAM failed: hr=0x%08lX", (unsigned long)hr);
        return hr;
    }
    logf("encoder: streaming (provides samples %d, output buffer %lu, sequence header %u bytes)",
         (int)e->providesSamples, (unsigned long)e->outSize, e->hdrLen);
    return S_OK;
}

mmEncW *newEnc(void *device, const mmencConfig *cfg) {
    mmEncW *e = (mmEncW *)calloc(1, sizeof(mmEncW));
    if (!e) return nullptr;
    e->cfg = *cfg;
    e->dev = (ID3D11Device *)device;
    e->dev->AddRef();
    e->dev->GetImmediateContext(&e->ctx);
    e->dev->QueryInterface(IID_PPV_ARGS(&e->mt));
    return e;
}

char *allocName(IMFActivate *a) {
    WCHAR *w = nullptr;
    UINT32 n = 0;
    char *out = (char *)calloc(1, 256);
    if (!out) return nullptr;
    if (SUCCEEDED(a->GetAllocatedString(MFT_FRIENDLY_NAME_Attribute, &w, &n)) && w) {
        WideCharToMultiByte(CP_UTF8, 0, w, -1, out, 255, nullptr, nullptr);
        CoTaskMemFree(w);
    } else {
        strcpy(out, "(unnamed MFT)");
    }
    return out;
}

ID3D11Texture2D *newPoolTexture(mmEncW *e) {
    D3D11_TEXTURE2D_DESC d;
    memset(&d, 0, sizeof d);
    d.Width = (UINT)e->cfg.width;
    d.Height = (UINT)e->cfg.height;
    d.MipLevels = 1;
    d.ArraySize = 1;
    d.Format = DXGI_FORMAT_NV12;
    d.SampleDesc.Count = 1;
    d.Usage = D3D11_USAGE_DEFAULT;
    // Some drivers want BIND_VIDEO_ENCODER on encoder input surfaces; not
    // all accept it. The first combination that works is kept.
    static const UINT binds[] = {D3D11_BIND_RENDER_TARGET | D3D11_BIND_VIDEO_ENCODER, D3D11_BIND_RENDER_TARGET, 0};
    for (int k = e->poolBindIdx; k < 3; k++) {
        d.BindFlags = binds[k];
        ID3D11Texture2D *t = nullptr;
        HRESULT hr = e->dev->CreateTexture2D(&d, nullptr, &t);
        if (SUCCEEDED(hr)) {
            if (!e->poolLogged) {
                e->poolLogged = true;
                logf("encoder: input pool NV12 %dx%d bind flags 0x%X", e->cfg.width, e->cfg.height, binds[k]);
            }
            e->poolBindIdx = k;
            return t;
        }
    }
    logf("encoder: CreateTexture2D(pool NV12 %dx%d) failed", e->cfg.width, e->cfg.height);
    return nullptr;
}

// checkTexture verifies the input texture belongs to the encoder's device
// and has the encoder's size.
HRESULT checkTexture(mmEncW *e, ID3D11Texture2D *t) {
    ID3D11Device *d = nullptr;
    t->GetDevice(&d);
    bool same = d == e->dev;
    release(d);
    if (!same) return MMENC_E_DEVICE;
    D3D11_TEXTURE2D_DESC desc;
    t->GetDesc(&desc);
    if ((int)desc.Width != e->cfg.width || (int)desc.Height != e->cfg.height || desc.Format != DXGI_FORMAT_NV12) {
        if (e->errLogs++ < 5)
            logf("encoder: input texture %ux%u format %d, encoder expects NV12 %dx%d", desc.Width, desc.Height,
                 (int)desc.Format, e->cfg.width, e->cfg.height);
        return MMENC_E_SIZE;
    }
    return S_OK;
}

HRESULT inputHW(mmEncW *e, ID3D11Texture2D *src, IMFSample **out) {
    int i = e->poolNext;
    e->poolNext = (e->poolNext + 1) % kPoolSize;
    if (!e->pool[i]) {
        e->pool[i] = newPoolTexture(e);
        if (!e->pool[i]) return E_OUTOFMEMORY;
    }
    if (!e->poolSample[i]) {
        IMFSample *s = nullptr;
        IMFMediaBuffer *b = nullptr;
        HRESULT hr = MFCreateSample(&s);
        if (SUCCEEDED(hr)) hr = MFCreateDXGISurfaceBuffer(__uuidof(ID3D11Texture2D), e->pool[i], 0, FALSE, &b);
        if (SUCCEEDED(hr)) {
            IMF2DBuffer *b2 = nullptr;
            DWORD len = 0;
            if (SUCCEEDED(b->QueryInterface(IID_PPV_ARGS(&b2)))) {
                b2->GetContiguousLength(&len);
                b2->Release();
            }
            b->SetCurrentLength(len);
            hr = s->AddBuffer(b);
        }
        release(b);
        if (FAILED(hr)) {
            release(s);
            logf("encoder: wrapping the input texture in an IMFSample failed: hr=0x%08lX", (unsigned long)hr);
            return hr;
        }
        e->poolSample[i] = s;
    }
    e->ctx->CopyResource(e->pool[i], src);
    *out = e->poolSample[i];
    (*out)->AddRef();
    return S_OK;
}

HRESULT inputSW(mmEncW *e, ID3D11Texture2D *src, IMFSample **out) {
    const int w = e->cfg.width, h = e->cfg.height;
    if (!e->staging) {
        D3D11_TEXTURE2D_DESC d;
        memset(&d, 0, sizeof d);
        d.Width = (UINT)w;
        d.Height = (UINT)h;
        d.MipLevels = 1;
        d.ArraySize = 1;
        d.Format = DXGI_FORMAT_NV12;
        d.SampleDesc.Count = 1;
        d.Usage = D3D11_USAGE_STAGING;
        d.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
        HRESULT hr = e->dev->CreateTexture2D(&d, nullptr, &e->staging);
        if (FAILED(hr)) {
            logf("encoder: CreateTexture2D(staging NV12) failed: hr=0x%08lX", (unsigned long)hr);
            return hr;
        }
    }
    IMFMediaBuffer *b = nullptr;
    DWORD size = (DWORD)(w * h * 3 / 2);
    HRESULT hr = MFCreateMemoryBuffer(size, &b);
    if (FAILED(hr)) return hr;
    if (e->mt) e->mt->Enter();
    e->ctx->CopyResource(e->staging, src);
    D3D11_MAPPED_SUBRESOURCE m;
    hr = e->ctx->Map(e->staging, 0, D3D11_MAP_READ, 0, &m);
    if (SUCCEEDED(hr)) {
        BYTE *dst = nullptr;
        hr = b->Lock(&dst, nullptr, nullptr);
        if (SUCCEEDED(hr)) {
            const uint8_t *y = (const uint8_t *)m.pData;
            const uint8_t *uv = y + (size_t)m.RowPitch * h;
            for (int r = 0; r < h; r++) memcpy(dst + (size_t)r * w, y + (size_t)r * m.RowPitch, (size_t)w);
            for (int r = 0; r < h / 2; r++)
                memcpy(dst + (size_t)w * h + (size_t)r * w, uv + (size_t)r * m.RowPitch, (size_t)w);
            b->Unlock();
            b->SetCurrentLength(size);
        }
        e->ctx->Unmap(e->staging, 0);
    }
    if (e->mt) e->mt->Leave();
    if (FAILED(hr)) {
        if (e->errLogs++ < 5) logf("encoder: reading the frame back to the CPU failed: hr=0x%08lX", (unsigned long)hr);
        b->Release();
        return hr;
    }
    IMFSample *s = nullptr;
    hr = MFCreateSample(&s);
    if (SUCCEEDED(hr)) hr = s->AddBuffer(b);
    b->Release();
    if (FAILED(hr)) {
        release(s);
        return hr;
    }
    *out = s;
    return S_OK;
}

} // namespace

extern "C" {

void mmenc_thread_init(void) { CoInitializeEx(nullptr, COINIT_MULTITHREADED); }

void mmenc_destroy(mmEncW *e) {
    if (!e) return;
    if (e->mft) e->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
    for (int i = 0; i < kPoolSize; i++) {
        release(e->poolSample[i]);
        release(e->pool[i]);
    }
    release(e->staging);
    release(e->outSample);
    release(e->api);
    release(e->shutdown);
    release(e->gen);
    release(e->mft);
    if (e->activate) {
        e->activate->ShutdownObject();
        release(e->activate);
    }
    release(e->mt);
    release(e->ctx);
    release(e->dev);
    free(e->buf);
    free(e->hdr);
    free(e);
}

int32_t mmenc_create_hw(void *device, void *manager, uint64_t luid, const mmencConfig *cfg, mmEncW **out, char *name,
                        int32_t nameLen) {
    *out = nullptr;
    if (nameLen > 0) name[0] = 0;
    mmenc_thread_init();
    MFT_REGISTER_TYPE_INFO inInfo = {MFMediaType_Video, MFVideoFormat_NV12};
    MFT_REGISTER_TYPE_INFO outInfo = {MFMediaType_Video, MFVideoFormat_H264};
    UINT32 flags = MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER;
    IMFActivate **acts = nullptr;
    UINT32 n = 0;
    HRESULT hr = E_FAIL;
    MFTEnum2Fn enum2 = nullptr;
    if (HMODULE m = GetModuleHandleW(L"mfplat.dll")) enum2 = (MFTEnum2Fn)(void *)GetProcAddress(m, "MFTEnum2");
    if (luid && !enum2) logf("encoder: MFTEnum2 unavailable; enumerating encoders of all adapters");
    if (luid && enum2) {
        IMFAttributes *a = nullptr;
        if (SUCCEEDED(MFCreateAttributes(&a, 1))) {
            a->SetUINT64(kMFT_ENUM_ADAPTER_LUID, luid);
            hr = enum2(MFT_CATEGORY_VIDEO_ENCODER, flags, &inInfo, &outInfo, a, &acts, &n);
            a->Release();
        }
        logf("encoder: MFTEnum2(hardware H.264, adapter %08lX:%08lX): hr=0x%08lX, %u MFT(s)",
             (unsigned long)(luid >> 32), (unsigned long)(luid & 0xffffffff), (unsigned long)hr, n);
    }
    if (FAILED(hr) || n == 0) {
        if (acts) CoTaskMemFree(acts);
        acts = nullptr;
        n = 0;
        hr = MFTEnumEx(MFT_CATEGORY_VIDEO_ENCODER, flags, &inInfo, &outInfo, &acts, &n);
        logf("encoder: MFTEnumEx(hardware H.264): hr=0x%08lX, %u MFT(s)", (unsigned long)hr, n);
    }
    if (FAILED(hr)) return hr;
    if (n == 0) return MF_E_TOPO_CODEC_NOT_FOUND;
    HRESULT last = MF_E_TOPO_CODEC_NOT_FOUND;
    mmEncW *found = nullptr;
    for (UINT32 i = 0; i < n; i++) {
        char *nm = allocName(acts[i]);
        logf("encoder: hardware MFT %u: %s", i, nm ? nm : "?");
        if (found) {
            free(nm);
            continue;
        }
        mmEncW *e = newEnc(device, cfg);
        if (!e) {
            free(nm);
            last = E_OUTOFMEMORY;
            continue;
        }
        e->hw = true;
        hr = acts[i]->ActivateObject(IID_PPV_ARGS(&e->mft));
        if (SUCCEEDED(hr)) {
            e->activate = acts[i];
            e->activate->AddRef();
            hr = configure(e, (IMFDXGIDeviceManager *)manager);
        } else {
            logf("encoder: ActivateObject failed: hr=0x%08lX", (unsigned long)hr);
        }
        if (SUCCEEDED(hr)) {
            found = e;
            if (nm && nameLen > 0) snprintf(name, (size_t)nameLen, "%s", nm);
        } else {
            logf("encoder: hardware MFT %u (%s) unusable: hr=0x%08lX", i, nm ? nm : "?", (unsigned long)hr);
            last = hr;
            mmenc_destroy(e);
        }
        free(nm);
    }
    for (UINT32 i = 0; i < n; i++) acts[i]->Release();
    CoTaskMemFree(acts);
    if (!found) return last;
    *out = found;
    return S_OK;
}

int32_t mmenc_create_sw(void *device, const mmencConfig *cfg, mmEncW **out) {
    *out = nullptr;
    mmenc_thread_init();
    mmEncW *e = newEnc(device, cfg);
    if (!e) return E_OUTOFMEMORY;
    HRESULT hr = CoCreateInstance(kCLSID_CMSH264EncoderMFT, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&e->mft));
    if (FAILED(hr)) {
        logf("encoder: CoCreateInstance(CMSH264EncoderMFT) failed: hr=0x%08lX (N edition without the Media "
             "Feature Pack?)",
             (unsigned long)hr);
        mmenc_destroy(e);
        return hr;
    }
    hr = configure(e, nullptr);
    if (FAILED(hr)) {
        mmenc_destroy(e);
        return hr;
    }
    *out = e;
    return S_OK;
}

int32_t mmenc_is_async(mmEncW *e) { return e->async ? 1 : 0; }

int32_t mmenc_can_block(mmEncW *e) { return e->shutdown ? 1 : 0; }

int32_t mmenc_get_event(mmEncW *e, int32_t wait, uint32_t *type, int32_t *status) {
    *type = 0;
    *status = 0;
    if (!e->gen) return E_UNEXPECTED;
    IMFMediaEvent *ev = nullptr;
    HRESULT hr = e->gen->GetEvent(wait ? 0 : MF_EVENT_FLAG_NO_WAIT, &ev);
    if (FAILED(hr)) return hr;
    MediaEventType t = 0;
    HRESULT st = S_OK;
    ev->GetType(&t);
    ev->GetStatus(&st);
    ev->Release();
    *type = (uint32_t)t;
    *status = st;
    return S_OK;
}

int32_t mmenc_input(mmEncW *e, void *texture, int64_t sampleTime, int64_t duration, int32_t forceKey) {
    ID3D11Texture2D *src = (ID3D11Texture2D *)texture;
    HRESULT hr = checkTexture(e, src);
    if (FAILED(hr)) return hr;
    IMFSample *s = nullptr;
    hr = e->hw ? inputHW(e, src, &s) : inputSW(e, src, &s);
    if (FAILED(hr)) return hr;
    s->SetSampleTime(sampleTime);
    s->SetSampleDuration(duration);
    if (forceKey) {
        HRESULT k = setU32(e->api, CODECAPI_AVEncVideoForceKeyFrame, 1);
        if (FAILED(k) && e->errLogs++ < 5)
            logf("encoder: CODECAPI_AVEncVideoForceKeyFrame failed: hr=0x%08lX", (unsigned long)k);
    }
    hr = e->mft->ProcessInput(e->inId, s, 0);
    s->Release();
    return hr;
}

int32_t mmenc_output(mmEncW *e, mmencOut *o) {
    memset(o, 0, sizeof *o);
    MFT_OUTPUT_DATA_BUFFER ob;
    memset(&ob, 0, sizeof ob);
    ob.dwStreamID = e->outId;
    if (!e->providesSamples) {
        if (!e->outSample) {
            IMFMediaBuffer *b = nullptr;
            HRESULT hr = MFCreateSample(&e->outSample);
            if (SUCCEEDED(hr)) hr = MFCreateMemoryBuffer(e->outSize, &b);
            if (SUCCEEDED(hr)) hr = e->outSample->AddBuffer(b);
            release(b);
            if (FAILED(hr)) {
                release(e->outSample);
                return hr;
            }
        }
        IMFMediaBuffer *b = nullptr;
        if (SUCCEEDED(e->outSample->GetBufferByIndex(0, &b))) {
            b->SetCurrentLength(0);
            b->Release();
        }
        ob.pSample = e->outSample;
    }
    DWORD status = 0;
    HRESULT hr = e->mft->ProcessOutput(0, 1, &ob, &status);
    release(ob.pEvents);
    if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
        IMFMediaType *t = nullptr;
        HRESULT h2 = e->mft->GetOutputAvailableType(e->outId, 0, &t);
        if (SUCCEEDED(h2)) {
            h2 = e->mft->SetOutputType(e->outId, t, 0);
            t->Release();
        }
        captureSeqHeader(e);
        logf("encoder: output stream change renegotiated: hr=0x%08lX (sequence header %u bytes)", (unsigned long)h2,
             e->hdrLen);
        if (e->providesSamples) release(ob.pSample);
        return FAILED(h2) ? h2 : MMENC_S_RETRY;
    }
    if (FAILED(hr)) {
        if (e->providesSamples) release(ob.pSample);
        return hr;
    }
    IMFSample *s = ob.pSample;
    if (!s) return E_UNEXPECTED;
    IMFMediaBuffer *b = nullptr;
    hr = s->ConvertToContiguousBuffer(&b);
    if (SUCCEEDED(hr)) {
        BYTE *p = nullptr;
        DWORD len = 0;
        hr = b->Lock(&p, nullptr, &len);
        if (SUCCEEDED(hr)) {
            if (len > e->bufCap) {
                uint8_t *nb = (uint8_t *)realloc(e->buf, len);
                if (nb) {
                    e->buf = nb;
                    e->bufCap = len;
                } else {
                    hr = E_OUTOFMEMORY;
                }
            }
            if (SUCCEEDED(hr)) {
                memcpy(e->buf, p, len);
                o->data = e->buf;
                o->len = (int32_t)len;
            }
            b->Unlock();
        }
        b->Release();
    }
    LONGLONG t = 0;
    if (SUCCEEDED(s->GetSampleTime(&t))) {
        o->sampleTime = t;
        o->hasTime = 1;
    }
    UINT32 clean = 0;
    if (SUCCEEDED(s->GetUINT32(MFSampleExtension_CleanPoint, &clean))) o->cleanPoint = clean ? 1 : 0;
    if (e->providesSamples) release(ob.pSample);
    return hr;
}

int32_t mmenc_seq_header(mmEncW *e, const uint8_t **data, int32_t *len) {
    *data = e->hdr;
    *len = (int32_t)e->hdrLen;
    return S_OK;
}

int32_t mmenc_set_bitrate(mmEncW *e, uint32_t bps) {
    e->cfg.bps = (int32_t)bps;
    return setU32(e->api, CODECAPI_AVEncCommonMeanBitRate, bps);
}

int32_t mmenc_set_gop(mmEncW *e, uint32_t frames) { return setU32(e->api, CODECAPI_AVEncMPVGOPSize, frames); }

int32_t mmenc_drain(mmEncW *e) {
    HRESULT hr = e->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_END_OF_STREAM, 0);
    HRESULT h2 = e->mft->ProcessMessage(MFT_MESSAGE_COMMAND_DRAIN, 0);
    return FAILED(h2) ? h2 : hr;
}

void mmenc_shutdown(mmEncW *e) {
    if (e->shutdown) e->shutdown->Shutdown();
}

} // extern "C"
