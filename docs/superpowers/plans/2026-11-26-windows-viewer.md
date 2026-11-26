# kkdesk 第 4 步：Windows 控制端（Windows 控 Mac）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Windows 上连接另一台电脑（Mac 或 Windows）时能看到画面并控制它，而不是收到第一帧就断开。

**Architecture:** 补上两个平台接口的 Windows 实现，`internal/app/viewer.go` 和 `internal/ui/session_window.go` 不改流程。`codec.NewDecoder` 用 Media Foundation H.264 解码器（`CLSID_MSH264DecoderMFT`，通过 `IMFDXGIDeviceManager` 在 `winmedia.Device()` 上硬解；没有 D3D11 解码能力时软解再上传），每帧裁掉对齐填充后拷进一张独立的 NV12 纹理交给回调。`display.View` 是一个自有的 HWND：Tango 的 `uihost.NativeHost` 把它挂成会话窗口的子窗口，并在 `PassInput` 下替它回答 `HTTRANSPARENT`，所以键鼠仍由 Tango 读取、沿用现有的 `forwardInput`。画面用 D3D11 视频处理器把 NV12 按等比缩放（黑边）画到 flip 模式交换链上。

**Tech Stack:** Go 1.27、cgo（C++17，本机 Windows 构建，`go env CC` 当前为 zig cc，TDM-GCC 也可用）、Media Foundation H.264 解码 MFT、D3D11 Video Processor、DXGI 1.2 flip 交换链（`CreateSwapChainForHwnd`）、`DwmGetCompositionTimingInfo`。

**Spec:** `docs/superpowers/specs/2026-11-24-kkdesk-spike-design.md`（第 103、104、151 行的 Windows 解码上屏和输入，推进顺序第 4 项「Windows 控制」）。第 3 步计划 `docs/superpowers/plans/2026-11-24-windows-host.md` 的 Global Constraints 继续适用（静态链接、只依赖系统 DLL、只用系统 API）。

## Global Constraints

- 只支持 Windows 10 1809+ / Windows 11 x64，构建标签 `windows && cgo`；非 cgo 构建继续走 `codec_other.go` / `display_other.go`。
- 链接方式同 `encode_windows.go`：`-static-libgcc -static-libstdc++ -static`，只依赖系统 DLL；新增的库只有 `-ldwmapi`（显示层）。
- 共享设备：解码输出纹理和交换链都在 `winmedia.Device()` 上（该设备已开多线程保护）。设备代际（`gen`）变化后，解码器和显示层各自在下一帧按新设备重建，不崩溃。
- 低延迟：解码器设 `CODECAPI_AVLowLatencyMode=TRUE`，每次 `ProcessInput` 后循环 `ProcessOutput` 直到 `MF_E_TRANSFORM_NEED_MORE_INPUT`，输出在 `Decode` 返回前送达（与 `codec.Decoder` 文档一致）。`Present` 用同步间隔 0，不阻塞解码线程。
- 颜色：NV12 BT.709 limited → BGRA 全范围，视频处理器设置输入/输出色彩空间，与编码端 `mmwm_conv` 对称。
- 日志前缀沿用现有风格：`kkdesk win: decoder: …`、`kkdesk win: display: …`，通过 `winmedia.Logf` 输出；选用硬件还是软件、原因、输出尺寸都要记一行。
- 环境变量 `KKDESK_DECODER=software` 强制软解（对应编码端的 `KKDESK_ENCODER=software`）。
- 界面文案、日志说明、文档用简体中文；代码注释用英文，风格同现有文件。

## Review Focus

1. **1080p 的对齐填充**：H.264 解码器输出 1920×1088，用户应看到 1920×1080，底部不能有绿边或花屏。→ Task 1 `TestDecode1080pCropped`。
2. **会话中途分辨率变化**（Mac 换分辨率、被控端换显示器）：新的 SPS 到来后画面按新尺寸继续，不能断开或定格。→ Task 1 `TestDecodeResolutionChange`。
3. **丢包后坏帧**：收到截断的数据时 `Decode` 返回错误（viewer 据此请求关键帧），下一个 IDR 必须能正常解出，解码器不能卡死。→ Task 1 `TestDecodeRecoversAfterCorruptFrame`。
4. **画面静止时调整窗口大小**：被控端画面不变时不出新帧，拖窗口、全屏、切「实际大小 1:1」后必须立刻按新尺寸重画上一帧，不能拉伸或黑屏。→ Task 2 `TestRepaintOnResize`。
5. **没有 GPU 解码的机器**（远程桌面会话、Basic Render Driver）：回退软解仍能看到画面，日志说明原因。→ Task 1 `TestDecodeSoftware`。

---

### Task 1: Media Foundation H.264 解码器

**Files:**
- Replace: `internal/codec/decode_windows.go`（现在只返回 `ErrUnsupported`）
- Create: `internal/codec/decode_windows.cpp`、`internal/codec/decode_windows.h`
- Create: `internal/codec/testutil_windows.go`（测试用：生成纯色 NV12 纹理、读回 NV12 纹理某点的 Y 值）
- Create: `internal/codec/codec_windows_test.go`
- Modify: `internal/codec/codec.go`（`Decoder`/`DecodeOutput` 文档补上 Windows 的像素格式；包注释去掉「解码未实现」）

**Interfaces:**
- Produces: `func NewDecoder(out DecodeOutput) (Decoder, error)`（Windows）。回调里的 `pix` 是 `winmedia.Device()` 上一张 **独立的** w×h NV12 `ID3D11Texture2D*`（不是纹理数组的某一片，已裁掉填充），只在回调期间有效；接收方要保留就自己 `AddRef` 或拷贝。`captureAt` 原样透传。
- Produces（测试辅助，仅 `_test` 可见以外也可放 `testutil_windows.go`）：`newSolidNV12(t, w, h int, y, u, v uint8) uintptr`、`lumaAt(t, tex uintptr, x, y int) uint8`、`textureSize(tex uintptr) (w, h int)`。
- Consumes: `winmedia.Device()`、`winmedia.ComInit()`、`winmedia.Logf`；测试里用现有的 Windows `NewEncoder` 生成码流。

C 接口（`decode_windows.h`，风格同 `encode_windows.h`）：
`mmdec_create(void *device, void *manager, int32_t software, mmDecW **out, char *info, int32_t infoLen)`、`mmdec_input(mmDecW*, const uint8_t *annexB, int32_t len, int64_t sampleTime)`、`mmdec_output(mmDecW*, mmdecOut *o)`（返回 `S_OK` 出一帧 / `MF_E_TRANSFORM_NEED_MORE_INPUT` / 流变化时内部重协商后返回 `MMDEC_S_RETRY`）、`mmdec_destroy(mmDecW*)`；`mmdecOut{void *texture; int32_t width, height; int64_t sampleTime;}`，其中 `texture` 是解码器持有的独立 NV12 纹理，到下一次 `mmdec_output` 前有效。

- [ ] **Step 1: 写测试辅助和失败的测试**（`codec_windows_test.go`，构建标签 `windows && cgo`）

```go
// 编码 n 帧纯色 NV12 → 解码，返回每个回调里的 (w, h, 中心 Y 值, captureAt)。
func roundTrip(t *testing.T, dec Decoder, w, h, n int, y uint8) []decoded

func TestDecode1080pCropped(t *testing.T) {
	// 1920×1080，Y=180,U=V=128，编码 30 帧
	// 期望：至少 25 帧输出；每帧 textureSize == (1920, 1080)；lumaAt 中心 ∈ [176,184]
	// 底部一行 (960, 1079) 的 Y ∈ [176,184]（填充行没有混进来）；captureAt 与输入一一相等
}
func TestDecodeResolutionChange(t *testing.T) {
	// 同一个 decoder：先 1280×720 编码 10 帧，再换新编码器 640×360 编码 10 帧
	// 期望：前段输出都是 1280×720，后段至少 8 帧是 640×360，中间无 Decode 错误
}
func TestDecodeRecoversAfterCorruptFrame(t *testing.T) {
	// 正常解 5 帧；把第 6 帧截成一半送入（允许返回 error，不允许 panic/阻塞超过 2s）
	// 再送一个强制关键帧（Encode(..., forceKey=true)）及之后 5 帧：期望全部有输出
}
func TestDecodeSliceBeforeSPS(t *testing.T) {
	// 新 decoder 直接送一个只含 P 帧 slice 的 access unit → 返回非 nil error，无回调
}
func TestDecodeSoftware(t *testing.T) {
	// t.Setenv("KKDESK_DECODER", "software")；重复 1080p 场景
	// 期望同 TestDecode1080pCropped；Info() 含 "software"
}
func TestDecodeAfterClose(t *testing.T) {
	// Close 两次不 panic；Close 后 Decode 返回 error
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/codec/ -run 'TestDecode' -v`
Expected: FAIL，`codec: not supported on this platform`

- [ ] **Step 3: 实现 `decode_windows.cpp`**

  - `mmdec_create`：`CoCreateInstance(CLSID_MSH264DecoderMFT)`；非软件模式且 `MF_SA_D3D11_AWARE` 为真时 `ProcessMessage(MFT_MESSAGE_SET_D3D_MANAGER, manager)`，失败则记日志并按软件模式继续；`CODECAPI_AVLowLatencyMode=TRUE`；输入类型 `MFVideoFormat_H264`（尺寸不设，由 SPS 决定）；输出类型从 `GetOutputAvailableType` 里选 `MFVideoFormat_NV12`。`info` 写成 `"MF H.264 decoder (D3D11)"` 或 `"MF H.264 decoder (software: <原因>)"`。
  - `mmdec_input`：Annex-B 原样放进一个 `IMFSample`（MS 解码器接受 Annex-B），`SetSampleTime(sampleTime)`。
  - `mmdec_output`：`MFT_OUTPUT_STREAM_PROVIDES_SAMPLES` 时不分配样本，否则按 `GetOutputStreamInfo.cbSize` 分配内存样本。`MF_E_TRANSFORM_STREAM_CHANGE` → 重新选 NV12 输出类型，读 `MF_MT_FRAME_SIZE` 和 `MF_MT_MINIMUM_DISPLAY_APERTURE`（有则以它为可见尺寸），返回 `MMDEC_S_RETRY`。
  - 输出拷贝：D3D11 路径从 `IMFDXGIBuffer` 取纹理和 `GetSubresourceIndex`，`CopySubresourceRegion` 只拷可见区域（`D3D11_BOX{0,0,0,w,h,1}`）到解码器持有的 w×h NV12 纹理（尺寸变化时重建）；软件路径从 `IMF2DBuffer::Lock2D` 按 pitch 拷 Y 平面 h 行、UV 平面 h/2 行，`UpdateSubresource` 上传到同一张纹理。
- [ ] **Step 4: 实现 `decode_windows.go`**：`mfDecoder` 带 `sync.Mutex`，`Decode` 锁内调用 `mmdec_input` 后循环 `mmdec_output`（`RETRY` 继续，`NEED_MORE_INPUT` 结束），每出一帧调 `out(uintptr(texture), time.Unix(0, sampleTime*100))`；`sampleTime = captureAt.UnixNano()/100`。只在第一次 SPS/PPS 出现之前遇到 slice 时返回 `codec: slice before SPS/PPS`（用 `splitAnnexB` 判断 NAL 类型，与 darwin 一致）。`winmedia.Device()` 的 `gen` 变化时在下一次 `Decode` 前重建解码器。提供 `Info() string`（测试和 viewer 日志用）。COM 用 `winmedia.ComInit()`，`Decode` 所在的 goroutine 不要求固定线程（MTA）。

- [ ] **Step 5: 运行测试通过**

Run: `go test ./internal/codec/ -v -count=1`
Expected: 新增 6 个测试 PASS，原有 `annexb` 测试仍 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/codec/
git commit -m "feat(codec): Media Foundation H.264 decoder on Windows"
```

---

### Task 2: Windows 显示视图

**Files:**
- Create: `internal/display/display_windows.go`、`internal/display/display_windows.cpp`、`internal/display/display_windows.h`
- Create: `internal/display/display_windows_test.go`
- Modify: `internal/display/display_other.go`（构建标签改为 `!(darwin || windows) || !cgo`）
- Modify: `internal/display/display.go`（包注释补上 Windows）

**Interfaces:**
- Consumes: Task 1 的回调像素（独立的 NV12 `ID3D11Texture2D*`）；`winmedia.Device()`。
- Produces（与 darwin 同名同签名，`session_window.go` 不改）：`func New() (*View, error)`（必须在 UI 主线程调用）、`(*View).Handle() uintptr`（HWND）、`Enqueue(pix uintptr, captureAt time.Time) (time.Time, error)`、`NextVsync(after time.Time) time.Time`、`FrameSize() (w, h int)`、`Release()`（幂等，主线程）。
- Produces（测试钩子，`display_windows.go` 内未导出）：`(*View).renderTo(tex uintptr) error`（把上一帧按当前客户区尺寸画进给定 BGRA 纹理，供读像素）、`(*View).presents() int`（已 Present 次数）。

- [ ] **Step 1: 写失败的测试**（`display_windows_test.go`，测试前 `runtime.LockOSThread()`，用 Task 1 同样的纯色 NV12 辅助——在本包里复制一个最小版本，不跨包引用测试文件）

```go
func TestEnqueueFrameSize(t *testing.T) {
	// New → Handle != 0；Enqueue 1280×720 纯色帧 → err == nil，FrameSize == (1280, 720)，
	// 返回的 enqueuedAt 在调用前后时间之间；Enqueue(0, …) 返回 error
}
func TestLetterbox(t *testing.T) {
	// SetWindowPos 把视图设为 800×800 客户区；Enqueue 1600×900 白帧（Y=235,U=V=128）
	// renderTo 一张 800×800 BGRA 纹理：(400, 100) 是黑色（上方黑边，VideoRect 给出 y=175）
	// (400, 400) 的 R,G,B 都 ≥ 240
}
func TestRepaintOnResize(t *testing.T) {
	// Enqueue 一帧后记 n := presents()；SetWindowPos 改尺寸 + SendMessage(WM_SIZE)
	// 期望 presents() > n（静止画面下按新尺寸重画），且不再调用 Enqueue
}
func TestNextVsyncWindows(t *testing.T) {
	// after := time.Now()；v := NextVsync(after)；期望 after < v ≤ after+50ms
}
func TestReleaseIdempotentWindows(t *testing.T) {
	// Release 两次不 panic；之后 Handle()==0，Enqueue 返回 error
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/display/ -v -count=1`
Expected: 编译失败或 FAIL（`display: unsupported platform`）

- [ ] **Step 3: 实现 `display_windows.cpp`**

  - 窗口：注册类 `KkdeskVideo`（`CS_HREDRAW|CS_VREDRAW`，背景刷为空），`CreateWindowExW(0, …, WS_POPUP|WS_CLIPSIBLINGS, …)` 隐藏创建，Tango 嵌入时会改成 `WS_CHILD` 并重新设父窗口。`GWLP_USERDATA` 指向显示对象。窗口过程处理 `WM_SIZE` 和 `WM_PAINT`：在临界区内重画上一帧并 `Present`（`WM_PAINT` 之后 `ValidateRect`），`WM_ERASEBKGND` 返回 1；其他交给 `DefWindowProcW`（Tango 会把窗口过程换成自己的，再链回这里）。
  - 交换链：`IDXGIFactory2::CreateSwapChainForHwnd`，`DXGI_FORMAT_B8G8R8A8_UNORM`、2 个缓冲、`DXGI_SWAP_EFFECT_FLIP_DISCARD`、`DXGI_SCALING_NONE`，尺寸取客户区（最小 1×1）。在第一次渲染时按帧纹理所属设备创建（`ID3D11DeviceChild::GetDevice`）；设备与当前交换链不同则整套重建。
  - 渲染（`Enqueue`、`WM_SIZE`、`WM_PAINT` 共用，持有一个 `CRITICAL_SECTION`）：客户区尺寸与缓冲区不同就 `ResizeBuffers`；把帧拷到显示层持有的 NV12「上一帧」纹理（尺寸变了就重建，视频处理器也按新输入尺寸重建）；清黑后 `VideoProcessorBlt`，目标矩形用与 `VideoRect` 相同的等比居中公式（取整到像素），输入色彩空间 BT.709 limited，输出 0–255 全范围；`Present(0, 0)`。`DXGI_ERROR_DEVICE_REMOVED/RESET` 时释放全部资源，下一帧重建。
  - 垂直同步：`DwmGetCompositionTimingInfo(NULL, …)` 取 `qpcVBlank`、`qpcRefreshPeriod`，与 `QueryPerformanceCounter` 现值一起返回给 Go。
- [ ] **Step 4: 实现 `display_windows.go`**：`View{mu sync.Mutex; d unsafe.Pointer; hwnd uintptr; w, h int}`，方法语义照抄 `display_darwin.go` 的文档；`NextVsync` 把 QPC 换算成 `time.Time` 后用现有 `nextTick`，DWM 不可用时返回 `after + fallbackPeriod`（`fallbackPeriod` 移到 `display.go` 共用，或在本文件另定义同值常量）。`#cgo LDFLAGS` 在 `encode_windows.go` 的基础上加 `-ldwmapi`。

- [ ] **Step 5: 运行测试通过**

Run: `go test ./internal/display/ -v -count=1`
Expected: 5 个新测试和原有 `display_test.go` 全部 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/display/
git commit -m "feat(display): Windows video view with a flip swap chain"
```

---

### Task 3: 接通会话、实机验证、更新文档

**Files:**
- Modify: `internal/app/viewer.go:446-451`（创建解码器后记一行日志：`kkdesk viewer: decoder: <Info()>`；失败时的日志保持现状）
- Modify: `docs/guide/introduction.md:60,63`、`docs/dev/architecture.md:25-26`、`README.md`（若有平台支持表）
- 不改：`internal/ui/session_window.go`（`display.New()` 在 Windows 上返回 HWND，Tango 负责嵌入和穿透输入）

**Interfaces:**
- Consumes: Task 1 的 `NewDecoder` + 可选 `Info() string`，Task 2 的 `display.View`。

- [ ] **Step 1: viewer 日志**：在 `v.dec = d` 之后，若 `d` 实现 `interface{ Info() string }` 就打印。运行 `go build ./... && go vet ./internal/...`，期望无输出。
- [ ] **Step 2: 全量测试**

Run: `go test ./... -count=1`
Expected: 全部 PASS。若出现计时相关的偶发失败，单独重跑该测试判断，不要放宽阈值。

- [ ] **Step 3: 实机验证（Windows 连 Mac，Mac 端运行当前 main 构建）**，逐项确认并把结果写进提交说明：
  1. 「控制」模式连接：10 秒内出画面，会话日志里有 `kkdesk viewer: decoder: MF H.264 decoder (D3D11)`，没有 `not supported`。
  2. 鼠标移动、点击、拖拽、滚轮、键盘输入（含中文输入法切换键以外的普通键、Ctrl+C/V）落点正确；在 125% 缩放下点击 Mac 菜单栏项目位置准确。
  3. 拖动窗口边缘、最大化、全屏、「实际大小 1:1」与「自适应」切换：画面立即按新尺寸显示且比例正确、黑边居中，Mac 画面静止时也一样。
  4. Mac 改分辨率（系统设置 → 显示器）后画面继续，不断开。
  5. 「仅查看」模式：画面正常，输入不转发。
  6. 关闭会话窗口后进程内存回落、再次连接正常（连续连接 3 次）。
  7. 会话统计里的帧率、延迟有值（窗口菜单或日志 `kkdesk viewer: stats`）。
- [ ] **Step 4: 更新文档**：`introduction.md` 表格里 Windows 的「控制端」一列改为「实机验证中」，说明改为「控制端：Media Foundation H.264 解码（优先硬件），D3D11 视频处理器上屏」；第 63 行删掉「Windows 作控制端看画面」。`architecture.md` 的 `internal/codec` 行改为「Windows：Media Foundation 编解码（优先硬件，回退软件）」，`internal/display` 行补上「Windows：自有 HWND + flip 交换链，由 Tango 嵌入会话窗口」，`internal/winmedia` 行改为「Windows 采集、编解码、显示共用的 Direct3D 11 设备」。
- [ ] **Step 5: 提交**

```bash
git add internal/app/viewer.go docs/ README.md
git commit -m "feat: Windows can view and control another computer"
```
