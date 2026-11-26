// Media Foundation H.264 decoder: the in-box CLSID_MSH264DecoderMFT,
// decoding into D3D11 textures through the shared IMFDXGIDeviceManager
// when the MFT is D3D11-aware, in system memory otherwise. Every decoded
// picture is copied (visible area only, without the 16-line alignment
// padding) into one decoder-owned NV12 texture on the shared device.

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <objbase.h>
#include <d3d11.h>
#include <mfapi.h>
#include <mferror.h>
#include <mfidl.h>
#include <mftransform.h>
#include <codecapi.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "decode_windows.h"

namespace {

// {62CE7E72-4C71-4D20-B15D-452831A87D9D}
const CLSID kCLSID_MSH264DecoderMFT = {0x62ce7e72, 0x4c71, 0x4d20, {0xb1, 0x5d, 0x45, 0x28, 0x31, 0xa8, 0x7d, 0x9d}};

void logf(const char *fmt, ...) {
    char buf[1024];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(buf, sizeof buf, fmt, ap);
    va_end(ap);
    mmdecLog(buf);
}

template <class T> void release(T *&p) {
    if (p) {
        p->Release();
        p = nullptr;
    }
}

} // namespace

struct mmDecW {
    IMFTransform *mft;
    ID3D11Device *dev;
    ID3D11DeviceContext *ctx;
    bool d3d;             // decoding into D3D11 textures
    bool providesSamples; // MFT_OUTPUT_STREAM_PROVIDES_SAMPLES
    DWORD outSize;        // cbSize of the output stream (system memory)
    UINT32 codedW, codedH; // MF_MT_FRAME_SIZE (with padding)
    UINT32 visW, visH;     // visible size (minimum display aperture)
    UINT32 visX, visY;
    LONG stride;           // system-memory row pitch (MF_MT_DEFAULT_STRIDE)
    IMFSample *outSample;  // system-memory output sample
    ID3D11Texture2D *pic;  // visW×visH NV12 picture handed out
    UINT32 picW, picH;
    uint8_t *tmp;          // repacked NV12 for UpdateSubresource
    size_t tmpCap;
    int errLogs;
    bool loggedSysOut;
};

namespace {

// selectOutput sets the NV12 output type and reads the frame geometry.
HRESULT selectOutput(mmDecW *d) {
    HRESULT hr = MF_E_INVALIDMEDIATYPE;
    for (DWORD i = 0;; i++) {
        IMFMediaType *t = nullptr;
        if (FAILED(d->mft->GetOutputAvailableType(0, i, &t))) break;
        GUID sub = GUID_NULL;
        t->GetGUID(MF_MT_SUBTYPE, &sub);
        if (sub == MFVideoFormat_NV12) {
            hr = d->mft->SetOutputType(0, t, 0);
            if (SUCCEEDED(hr)) {
                UINT32 w = 0, h = 0;
                MFGetAttributeSize(t, MF_MT_FRAME_SIZE, &w, &h);
                d->codedW = w;
                d->codedH = h;
                d->visX = d->visY = 0;
                d->visW = w;
                d->visH = h;
                MFVideoArea a;
                if (SUCCEEDED(t->GetBlob(MF_MT_MINIMUM_DISPLAY_APERTURE, (UINT8 *)&a, sizeof a, nullptr)) &&
                    a.Area.cx > 0 && a.Area.cy > 0) {
                    d->visX = (UINT32)a.OffsetX.value;
                    d->visY = (UINT32)a.OffsetY.value;
                    d->visW = (UINT32)a.Area.cx;
                    d->visH = (UINT32)a.Area.cy;
                }
                UINT32 stride = 0;
                d->stride = SUCCEEDED(t->GetUINT32(MF_MT_DEFAULT_STRIDE, &stride)) ? (LONG)stride : (LONG)w;
                logf("decoder: output NV12 %ux%u, visible %ux%u at (%u,%u)", w, h, d->visW, d->visH, d->visX, d->visY);
            }
            t->Release();
            break;
        }
        t->Release();
    }
    if (FAILED(hr)) {
        logf("decoder: no NV12 output type: hr=0x%08lX", (unsigned long)hr);
        return hr;
    }
    MFT_OUTPUT_STREAM_INFO si;
    memset(&si, 0, sizeof si);
    hr = d->mft->GetOutputStreamInfo(0, &si);
    if (FAILED(hr)) return hr;
    d->providesSamples = (si.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES | MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES)) != 0;
    d->outSize = si.cbSize;
    release(d->outSample);
    return S_OK;
}

HRESULT ensurePicture(mmDecW *d) {
    if (d->pic && d->picW == d->visW && d->picH == d->visH) return S_OK;
    release(d->pic);
    D3D11_TEXTURE2D_DESC t;
    memset(&t, 0, sizeof t);
    t.Width = d->visW;
    t.Height = d->visH;
    t.MipLevels = 1;
    t.ArraySize = 1;
    t.Format = DXGI_FORMAT_NV12;
    t.SampleDesc.Count = 1;
    t.Usage = D3D11_USAGE_DEFAULT;
    t.BindFlags = D3D11_BIND_SHADER_RESOURCE;
    HRESULT hr = d->dev->CreateTexture2D(&t, nullptr, &d->pic);
    if (FAILED(hr)) {
        logf("decoder: CreateTexture2D(NV12 %ux%u) failed: hr=0x%08lX", d->visW, d->visH, (unsigned long)hr);
        return hr;
    }
    d->picW = d->visW;
    d->picH = d->visH;
    return S_OK;
}

// copyD3D copies the visible area of a decoded texture (array slice) into pic.
HRESULT copyD3D(mmDecW *d, IMFMediaBuffer *buf) {
    IMFDXGIBuffer *dx = nullptr;
    HRESULT hr = buf->QueryInterface(IID_PPV_ARGS(&dx));
    if (FAILED(hr)) return hr;
    ID3D11Texture2D *src = nullptr;
    UINT sub = 0;
    hr = dx->GetResource(IID_PPV_ARGS(&src));
    if (SUCCEEDED(hr)) hr = dx->GetSubresourceIndex(&sub);
    if (SUCCEEDED(hr)) {
        D3D11_BOX box = {d->visX, d->visY, 0, d->visX + d->visW, d->visY + d->visH, 1};
        d->ctx->CopySubresourceRegion(d->pic, 0, 0, 0, 0, src, sub, &box);
    }
    release(src);
    release(dx);
    return hr;
}

// copySystem uploads the visible area of a system-memory NV12 picture.
HRESULT copySystem(mmDecW *d, IMFMediaBuffer *buf) {
    BYTE *base = nullptr;
    LONG pitch = 0;
    IMF2DBuffer *b2 = nullptr;
    DWORD maxLen = 0, curLen = 0;
    HRESULT hr;
    if (SUCCEEDED(buf->QueryInterface(IID_PPV_ARGS(&b2)))) {
        hr = b2->Lock2D(&base, &pitch);
    } else {
        hr = buf->Lock(&base, &maxLen, &curLen);
        pitch = d->stride > 0 ? d->stride : (LONG)d->codedW;
    }
    if (FAILED(hr)) {
        release(b2);
        return hr;
    }
    // Y plane codedH rows, then the interleaved UV plane, same pitch.
    size_t w = d->visW, h = d->visH;
    size_t need = w * h * 3 / 2;
    if (d->tmpCap < need) {
        free(d->tmp);
        d->tmp = (uint8_t *)malloc(need);
        d->tmpCap = d->tmp ? need : 0;
    }
    if (d->tmp) {
        const BYTE *y = base + (size_t)d->visY * pitch + d->visX;
        const BYTE *uv = base + (size_t)pitch * d->codedH + (size_t)(d->visY / 2) * pitch + d->visX;
        for (size_t r = 0; r < h; r++) memcpy(d->tmp + r * w, y + r * pitch, w);
        for (size_t r = 0; r < h / 2; r++) memcpy(d->tmp + (h + r) * w, uv + r * pitch, w);
        d->ctx->UpdateSubresource(d->pic, 0, nullptr, d->tmp, (UINT)w, (UINT)(w * h * 3 / 2));
    } else {
        hr = E_OUTOFMEMORY;
    }
    if (b2) {
        b2->Unlock2D();
        b2->Release();
    } else {
        buf->Unlock();
    }
    return hr;
}

} // namespace

int32_t mmdec_create(void *device, void *manager, int32_t software, mmDecW **out, char *info, int32_t infoLen) {
    *out = nullptr;
    mmDecW *d = (mmDecW *)calloc(1, sizeof(mmDecW));
    if (!d) return E_OUTOFMEMORY;
    d->dev = (ID3D11Device *)device;
    d->dev->AddRef();
    d->dev->GetImmediateContext(&d->ctx);
    HRESULT hr = CoCreateInstance(kCLSID_MSH264DecoderMFT, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&d->mft));
    if (FAILED(hr)) {
        logf("decoder: CoCreateInstance(MSH264DecoderMFT) failed: hr=0x%08lX", (unsigned long)hr);
        mmdec_destroy(d);
        return hr;
    }
    const char *why = software ? "KKDESK_DECODER=software" : nullptr;
    IMFAttributes *attr = nullptr;
    if (SUCCEEDED(d->mft->GetAttributes(&attr)) && attr) {
        attr->SetUINT32(CODECAPI_AVLowLatencyMode, TRUE);
        if (!software) {
            UINT32 aware = 0;
            if (FAILED(attr->GetUINT32(MF_SA_D3D11_AWARE, &aware)) || !aware) why = "MFT is not D3D11-aware";
        }
        attr->Release();
    } else if (!software) {
        why = "MFT has no attributes";
    }
    char reason[160];
    if (!why) {
        hr = d->mft->ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)manager);
        if (SUCCEEDED(hr)) {
            d->d3d = true;
        } else {
            snprintf(reason, sizeof reason, "SET_D3D_MANAGER failed: hr=0x%08lX", (unsigned long)hr);
            why = reason;
        }
    }
    IMFMediaType *in = nullptr;
    hr = MFCreateMediaType(&in);
    if (SUCCEEDED(hr)) {
        in->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Video);
        in->SetGUID(MF_MT_SUBTYPE, MFVideoFormat_H264);
        in->SetUINT32(MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
        hr = d->mft->SetInputType(0, in, 0);
        in->Release();
    }
    if (FAILED(hr)) {
        logf("decoder: SetInputType(H.264) failed: hr=0x%08lX", (unsigned long)hr);
        mmdec_destroy(d);
        return hr;
    }
    hr = selectOutput(d);
    if (FAILED(hr)) {
        mmdec_destroy(d);
        return hr;
    }
    d->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0);
    d->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
    if (d->d3d)
        snprintf(info, infoLen, "MF H.264 decoder (D3D11)");
    else
        snprintf(info, infoLen, "MF H.264 decoder (software: %s)", why);
    logf("decoder: %s", info);
    *out = d;
    return S_OK;
}

int32_t mmdec_input(mmDecW *d, const uint8_t *annexB, int32_t len, int64_t sampleTime) {
    IMFMediaBuffer *buf = nullptr;
    HRESULT hr = MFCreateMemoryBuffer((DWORD)len, &buf);
    if (FAILED(hr)) return hr;
    BYTE *p = nullptr;
    hr = buf->Lock(&p, nullptr, nullptr);
    if (SUCCEEDED(hr)) {
        memcpy(p, annexB, (size_t)len);
        buf->Unlock();
        buf->SetCurrentLength((DWORD)len);
    }
    IMFSample *s = nullptr;
    if (SUCCEEDED(hr)) hr = MFCreateSample(&s);
    if (SUCCEEDED(hr)) hr = s->AddBuffer(buf);
    if (SUCCEEDED(hr)) {
        s->SetSampleTime(sampleTime);
        hr = d->mft->ProcessInput(0, s, 0);
    }
    release(s);
    release(buf);
    if (FAILED(hr) && hr != MF_E_NOTACCEPTING && d->errLogs < 10) {
        d->errLogs++;
        logf("decoder: ProcessInput failed: hr=0x%08lX", (unsigned long)hr);
    }
    return hr;
}

int32_t mmdec_output(mmDecW *d, mmdecOut *o) {
    memset(o, 0, sizeof *o);
    HRESULT hr;
    if (!d->providesSamples && !d->outSample) {
        IMFMediaBuffer *buf = nullptr;
        hr = MFCreateMemoryBuffer(d->outSize ? d->outSize : d->codedW * d->codedH * 3 / 2, &buf);
        if (SUCCEEDED(hr)) hr = MFCreateSample(&d->outSample);
        if (SUCCEEDED(hr)) hr = d->outSample->AddBuffer(buf);
        release(buf);
        if (FAILED(hr)) {
            release(d->outSample);
            return hr;
        }
    }
    if (d->outSample) {
        // The decoder writes after the buffer's current length: reset it.
        IMFMediaBuffer *b = nullptr;
        if (SUCCEEDED(d->outSample->GetBufferByIndex(0, &b))) {
            b->SetCurrentLength(0);
            b->Release();
        }
    }
    MFT_OUTPUT_DATA_BUFFER db;
    memset(&db, 0, sizeof db);
    db.pSample = d->providesSamples ? nullptr : d->outSample;
    DWORD status = 0;
    hr = d->mft->ProcessOutput(0, 1, &db, &status);
    if (db.pEvents) db.pEvents->Release();
    if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
        hr = selectOutput(d);
        return FAILED(hr) ? hr : MMDEC_S_RETRY;
    }
    if (FAILED(hr)) {
        if (d->providesSamples && db.pSample) db.pSample->Release();
        return hr;
    }
    IMFSample *s = db.pSample;
    IMFMediaBuffer *buf = nullptr;
    hr = s ? s->GetBufferByIndex(0, &buf) : E_POINTER;
    if (SUCCEEDED(hr)) hr = ensurePicture(d);
    if (SUCCEEDED(hr)) {
        // Decide per sample: even with a device manager the decoder may fall
        // back to system-memory output (no H.264 decode profile on the GPU).
        IMFDXGIBuffer *dx = nullptr;
        bool gpu = SUCCEEDED(buf->QueryInterface(IID_PPV_ARGS(&dx)));
        release(dx);
        if (d->d3d && !gpu && !d->loggedSysOut) {
            d->loggedSysOut = true;
            logf("decoder: D3D11 decoding was set up but the decoder outputs system memory; uploading each picture");
        }
        hr = gpu ? copyD3D(d, buf) : copySystem(d, buf);
    }
    if (SUCCEEDED(hr)) {
        LONGLONG t = 0;
        s->GetSampleTime(&t);
        o->texture = d->pic;
        o->width = (int32_t)d->visW;
        o->height = (int32_t)d->visH;
        o->sampleTime = t;
    } else if (d->errLogs < 10) {
        d->errLogs++;
        logf("decoder: copying the decoded picture failed: hr=0x%08lX", (unsigned long)hr);
    }
    release(buf);
    if (d->providesSamples && s) s->Release();
    return hr;
}

void mmdec_destroy(mmDecW *d) {
    if (!d) return;
    if (d->mft) {
        d->mft->ProcessMessage(MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
        d->mft->ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER, 0);
    }
    release(d->outSample);
    release(d->pic);
    release(d->mft);
    release(d->ctx);
    release(d->dev);
    free(d->tmp);
    free(d);
}
