# kkdesk Mac 自连验证 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在一台 Mac 上跑通 kkdesk 的完整链路：`kkdesk -host` 采集屏幕 → H.264 硬件编码 → pion WebRTC → `kkdesk -connect 127.0.0.1` 硬件解码 → Tango `NativeHost` 原生视图上屏，鼠标键盘反向注入；记录分段延迟、帧率、码率和 CPU，写出第一份测量报告。

**Architecture:** 一个 Go 程序两种角色。平台相关部分（采集、编解码、显示、注入）用 cgo 调 macOS 框架，放在 `internal/*/..._darwin.go`，对外是平台无关的 Go 接口，Windows 版后续步骤按同一接口实现。传输、信令、输入协议、统计是纯 Go，可在任何平台单测。界面用 Tango 自绘（`uihost.Open`），远程画面用 `uihost.NativeHost` 嵌入 `AVSampleBufferDisplayLayer` 视图，会话菜单用 `sys/menu` 的 `menu.Popup`。

**Tech Stack:** Go 1.27、cgo（Objective-C，ScreenCaptureKit / VideoToolbox / AVFoundation / CoreGraphics）、`github.com/pion/webrtc/v4` v4.2.11、Tango（`github.com/tangoui/tango`，本地 `/Users/admin/min/tango`，需含 ed116886：`KeyEvent.HID`、`Context.PointerEvents`、`uihost.NativeHost`）。

**Spec:** `docs/superpowers/specs/2026-11-24-kkdesk-spike-design.md`（第 2、5、7、8 节，推进顺序第 2 项）。

## Global Constraints

- 模块路径 `kkdesk`；`go.mod` 用 `replace github.com/tangoui/tango => ../tango`（本地 Tango 主工作区，已在 ed116886 之后）。编译 macOS 部分需 `CGO_ENABLED=1`（本机默认是 0）。
- 不复制 RustDesk / ToDesk / UU 的任何代码；只用系统框架和 pion。
- 编码：H.264，VideoToolbox 硬件编码，`kVTCompressionPropertyKey_RealTime=true`、不允许帧重排（无 B 帧）、`MaxKeyFrameInterval` = 帧率×2、Baseline 或 Main（ConstrainedBaseline 优先，便于 WebRTC 协商）；收到 PLI 立即出关键帧。码率档位：自动（起步 8 Mbps，跟随 pion 带宽估计）、30/14/8/3 Mbps；帧率档位 30/60/90/144，不超过屏幕刷新率。
- 采集：ScreenCaptureKit，主屏，输出 NV12（`kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange`），宽度超过 1920 时按比例缩到 1920 宽（偶数对齐），`minimumFrameInterval` = 1/帧率，`queueDepth` 小（3），排除所有名为 kkdesk 的进程的窗口（避免自连时画面套娃）；画面不变时不出新帧。
- 显示：控制端用 `VTDecompressionSession` 硬解出 `CVPixelBuffer`，包成带 `kCMSampleAttachmentKey_DisplayImmediately` 的 `CMSampleBuffer` 交给 `AVSampleBufferDisplayLayer`；视图通过 `uihost.NativeHost`（`PassInput: true`）嵌入，画面按比例适配盒子。
- 传输：pion 视频轨道 H.264（packetization-mode=1）；每帧带采集时间戳：用一个自定义 RTP 头扩展或随帧通过数据通道发送 `{rtpTimestamp → captureUnixNano}`，二选一，以实现简单为准；局域网只用 host 候选。输入两条数据通道：`input-move`（无序、`MaxRetransmits=0`）和 `input`（有序可靠）。
- 信令：被控端监听 TCP（默认 21118），显示本机 IP 和 6 位数字临时密码；控制端连接后做 HMAC-SHA256 挑战应答（服务端发随机 nonce，客户端回 HMAC(password, nonce)），通过后交换 SDP（JSON 行协议），完成后 TCP 可关闭。
- 输入协议：二进制小包。鼠标坐标归一化到 [0,1]（相对被控端主屏）；键盘发 `input.KeyEvent.HID`（无 HID 时不发）+ 按下/松开；滚轮发逻辑像素增量。被控端 HID → macOS virtual keycode（反查 Tango 的映射思路，kkdesk 内自写表）后用 `CGEventPost` 注入；需要辅助功能权限，首次启动检测并提示。自连时默认「仅查看」，注入由会话菜单开关打开（避免本机鼠标被自己的注入抢走）。
- 测量：被控端每帧记录采集完成、编码完成、发出时间；控制端记录收齐、解码完成、送显时间，并用 `CVDisplayLink` 取送显后的下一个刷新时刻作为上屏时间。自连同一时钟，不做时钟同步（跨机时钟同步留到 Windows 步骤）。每秒统计帧率、码率、丢包；两端各每秒采样 CPU：本进程（`getrusage`）+ `VTDecoderXPCService`、`VTEncoderXPCService`、`WindowServer` 的增量（`ps -o %cpu`，或 `proc_pidinfo`）。日志写 JSON Lines 到 `-log` 指定文件。
- 界面：验证用最小界面，风格按 `/Users/admin/min/outputs/remote-desktop-ui/index.html`：主窗口（本机 IP、临时密码可刷新、允许被控开关；对方 IP + 密码 + 连接按钮）；会话窗口（标签栏右侧延迟/帧率 + 「控制中心」按钮弹出原生菜单：画质码率档位、帧率档位、真彩/HDR/中继灰显、仅查看/控制切换、退出远控；下方整块 NativeHost）；被控端有人连入时弹确认，连接期间右下角面板可断开。
- 提交：仓库 `/Users/admin/min/kkdesk`，作者用全局 git 身份，结尾加 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`；不推送（尚无远端）。
- GUI 核对按后台方式进行（`TANGO_LAUNCH_BACKGROUND=1`，cua-driver 后台操作），不抢前台；需要屏幕录制 / 辅助功能权限时告诉用户由用户授权。
- 编译测试用 `nice -n 19`。

## Review Focus

1. 被控端屏幕静止很久后突然变化：第一帧要立即发出，控制端要能解（必要时补关键帧）；控制端中途丢包后通过 PLI 恢复，不出现长时间花屏或卡死。
2. 控制端窗口缩放、切到别的 Retina/非 Retina 屏：画面按比例适配，鼠标坐标换算到被控端仍然准确（归一化坐标以视频画面区域为准，不含黑边）。
3. 断开：任一端退出或网络断开，另一端在几秒内回到可再次连接的状态，不泄漏编码器、采集会话、PeerConnection。
4. 错误密码、重复连接、被控端拒绝：控制端看到清楚的错误信息，被控端不崩溃、不卡住。
5. 没有屏幕录制或辅助功能权限：启动时说明需要哪项权限和去哪里打开，而不是黑屏或静默失败。

---

### Task 1: 仓库骨架、输入协议与键码映射（纯 Go）

**Files:**
- Create: `go.mod`、`go.work`（如需要）、`.gitignore`、`main.go（项目根目录：tan dev / tan package 编译根目录的 main 包）`（暂时只解析参数、打印用法）
- Create: `internal/input/proto.go`、`internal/input/proto_test.go`
- Create: `internal/input/keymap_darwin_table.go`（无构建标签，纯表）、`internal/input/keymap_test.go`

**Interfaces:**
- Produces:
  - `type Event struct{ Kind Kind; X, Y float32; Button uint8; Down bool; HID uint16; DX, DY float32; Mods uint8 }`，`Kind`：`Move`、`Button`、`Key`、`Wheel`。
  - `func (e Event) Marshal(dst []byte) []byte`、`func Unmarshal(b []byte) (Event, error)`。
  - `func MacKeyCode(hid uint16) (code uint16, ok bool)` —— HID → macOS virtual keycode。

- [ ] 写测试：每种 Kind 的往返编解码；截断/未知类型返回错误；`MacKeyCode` 覆盖 A(0x04→0x00)、Enter(0x28→0x24)、左右 Shift/Ctrl/Alt/GUI、F1–F20、方向键、小键盘、ISO 0x64→0x0A、JIS 0x89→0x5D/0x87→0x5E/0x90→0x68/0x91→0x66，未知返回 false；并断言表与 Tango `internal/platform/darwin` 的 `macHIDKeys` 互为反函数（把该表的条目复制为测试数据，不导入内部包）。
- [ ] 运行确认失败 → 实现 → 运行确认通过：`go test ./internal/input/...`。
- [ ] 提交 `feat: input protocol and HID to macOS key code map`。

### Task 2: 信令与 WebRTC 会话（纯 Go）

**Files:**
- Create: `internal/session/signal.go`、`internal/session/peer.go`、`internal/session/session_test.go`

**Interfaces:**
- Produces:
  - `type Host struct{...}`：`func Listen(addr, password string) (*Host, error)`；`func (h *Host) Accept(ctx) (*Peer, error)`（挑战应答失败返回 `ErrBadPassword` 并继续监听）。
  - `func Dial(ctx, addr, password string) (*Peer, error)`。
  - `type Peer`：被控端 `WriteVideo(nal []byte, captureAt time.Time, keyframe bool) error`；控制端 `OnVideo(func(frame []byte, captureAt time.Time))`（按帧组好的 Annex-B）；双方 `SendInput(input.Event) error` / `OnInput(func(input.Event))`；`OnKeyframeRequest(func())`（被控端，收到 PLI）；`RequestKeyframe()`（控制端）；`Stats() Stats`；`Close() error`；`Done() <-chan struct{}`。
- 测试：进程内 Host+Dial 走 127.0.0.1：错误密码被拒且 Host 继续可用；正确密码建立连接；发送若干个合成 H.264 帧（可用固定的 SPS/PPS/IDR 字节）和输入事件，对端按序收到、captureAt 一致；控制端 `RequestKeyframe` 触发被控端回调；任一端 Close 后另一端 `Done` 在 5 秒内关闭。
- [ ] 测试先行 → 实现 → `go test -race ./internal/session/...` 通过 → 提交 `feat: signaling with password challenge and WebRTC peer`。

### Task 3: VideoToolbox 编码与解码（cgo，macOS）

**Files:**
- Create: `internal/codec/codec.go`（接口）、`internal/codec/encode_darwin.go`、`internal/codec/decode_darwin.go`、`internal/codec/codec_darwin_test.go`

**Interfaces:**
- Produces:
  - `type Encoder interface{ Encode(frame Frame, forceKey bool) error; SetBitrate(bps int); SetFPS(fps int); Close() }`，输出回调 `func(nal []byte, captureAt time.Time, keyframe bool)`（Annex-B，关键帧前带 SPS/PPS）。`Frame` 包装平台像素缓冲（darwin：`CVPixelBufferRef` 的 uintptr）与 `CaptureAt`。
  - `func NewEncoder(w, h, fps, bps int, out func(...)) (Encoder, error)`。
  - `type Decoder interface{ Decode(annexB []byte, captureAt time.Time) error; Close() }`，输出回调 `func(pix uintptr /*CVPixelBufferRef，回调内有效*/, captureAt time.Time)`；`func NewDecoder(out func(...)) (Decoder, error)`（从 SPS/PPS 建会话，分辨率变化时重建）。
- 测试（无需权限）：用 CoreVideo 在测试里创建 1920×1080 NV12 像素缓冲（填渐变），编码 60 帧 → 第一帧是关键帧且含 SPS/PPS → 全部送进解码器 → 解出 60 帧、尺寸一致；`forceKey` 生效；记录每帧编码耗时并打印中位数。
- [ ] 测试先行 → 实现 → `CGO_ENABLED=1 go test ./internal/codec/...` → 提交 `feat: VideoToolbox H.264 encoder and decoder`。

### Task 4: ScreenCaptureKit 采集（cgo，macOS）

**Files:**
- Create: `internal/capture/capture.go`（接口）、`internal/capture/capture_darwin.go`、`internal/capture/capture_darwin_test.go`

**Interfaces:**
- Produces: `func Start(opts Options, out func(Frame)) (Session, error)`；`Options{FPS int; MaxWidth int; ExcludeApp string}`；`Session{ SetFPS(int); Close(); Size() (w, h int) }`；`func HasPermission() bool`、`func RequestPermission()`。
- 测试：`HasPermission()` 为 false 时 `t.Skip`（打印如何授权）；为 true 时采集 2 秒，断言收到帧、尺寸宽 ≤ 1920 且为偶数、像素格式 NV12、CaptureAt 单调递增。
- [ ] 实现 → 测试 → 提交 `feat: ScreenCaptureKit capture`。

### Task 5: 显示视图与输入注入（cgo，macOS）

**Files:**
- Create: `internal/display/display_darwin.go`（`AVSampleBufferDisplayLayer` 承载的 NSView：`New() (view uintptr)`、`Enqueue(pix uintptr, captureAt) (enqueuedAt time.Time)`、`NextVsync() time.Time`、`VideoRect(boxW, boxH) Rect`（按比例适配后的画面区域）、`Release()`）
- Create: `internal/inject/inject_darwin.go`（`HasPermission()`、`RequestPermission()`、`Apply(input.Event, screenW, screenH)`：鼠标移动/按键/滚轮用 CGEvent，键盘用 `MacKeyCode`）
- Test: `internal/display/display_darwin_test.go`（创建视图、入队几帧不崩溃、`VideoRect` 纯计算单测）；`internal/inject` 只单测坐标换算（纯函数），真实注入不在自动测试里做。

- [ ] 实现 → 测试 → 提交 `feat: display layer view and CGEvent injection`。

### Task 6: 统计与日志

**Files:**
- Create: `internal/stats/stats.go`、`internal/stats/cpu_darwin.go`、`internal/stats/stats_test.go`

**Interfaces:**
- Produces: `type FrameTimes struct{ Seq uint64; Capture, Encoded, Sent, Received, Decoded, Enqueued, Presented time.Time }`；`Recorder`：`Frame(FrameTimes)`、每秒汇总 `Second{FPS, BitrateBps, LossPct, LatencyP50, LatencyP95 time.Duration, Segments map[string]time.Duration(各段中位数), CPU map[string]float64}`，写 JSON Lines；`func Summary(path string) (Report, error)` 读日志算全程中位数/P95。
- 测试：喂合成帧时间，P50/P95 与分段中位数正确；CPU 采样函数在本进程返回非负值。
- [ ] 测试先行 → 实现 → 提交 `feat: latency, fps and CPU stats`。

### Task 7: 应用：界面与两端接线

**Files:**
- Create: `internal/app/host.go`（采集→编码→Peer；PLI→强制关键帧；输入→注入；连接确认与右下角面板）
- Create: `internal/app/viewer.go`（Peer→解码→显示；指针/按键→输入事件；控制中心菜单）
- Create: `internal/ui/main_window.go`、`internal/ui/session_window.go`
- Modify: `main.go（项目根目录：tan dev / tan package 编译根目录的 main 包）`（`-host`、`-connect addr`、`-password`、`-fps`、`-bitrate`、`-log`、`-autoaccept`（测量用）、`-view-only`）

- [ ] 实现；`CGO_ENABLED=1 go build .`。
- [ ] 后台实机：`kkdesk -host -autoaccept -log host.jsonl` 与 `kkdesk -connect 127.0.0.1 -password … -log view.jsonl`，确认会话窗口出现画面（cua-driver 后台截图）、帧率与延迟在标签栏显示；打开控制后后台发一次点击，被控端日志记录注入。
- [ ] 提交 `feat: kkdesk app wiring host and viewer`。

### Task 8: 自连测量与报告

**Files:**
- Create: `scripts/loopback.sh`（启动两端、在被控屏幕上播放持续变化的内容 60 秒、收集日志）
- Create: `docs/spike-report.md`（本步结果：各段中位数/P95、帧率、码率、CPU 两种口径；与成功标准对照；问题与下一步）

- [ ] 跑 1080p60（自动码率）和原生分辨率参考各一次；如本机显示器支持更高刷新率再跑 90/120。
- [ ] 归档原始日志到 `/Users/admin/min/outputs/kkdesk-spike/loopback-<日期>/`。
- [ ] 提交 `docs: loopback measurements`。
