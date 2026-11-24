# mmdesk 第一步：验证原型设计

日期：2026-11-24
状态：待用户确认

## 1. 目的

mmdesk 是一个用 Tango 开发的远程桌面客户端（参考 RustDesk、ToDesk、UU 远程的产品形态，只借思路，不使用其代码；RustDesk 为 AGPL-3.0）。界面原型见 `/Users/admin/min/outputs/remote-desktop-ui/index.html`。

第一步只回答一个问题：**用 Tango + Go 能不能做出延迟和占用都达标的远程桌面核心链路**。做法是在局域网内让 Mac 和 Windows 互相控制，实测延迟、帧率和 CPU，并跑通 Tango 原生视频视图这条显示路线。

## 2. 成功标准

在同一局域网（有线或 5 GHz Wi-Fi）内，1920×1080、60 fps 桌面画面：

| 指标 | 目标 |
|---|---|
| 端到端延迟（被控端采集 → 控制端上屏）中位数 | ≤ 50 ms |
| 端到端延迟 P95 | ≤ 80 ms |
| 画面持续变化时的实际帧率 | ≥ 55 fps |
| 被控端 CPU（采集 + 编码） | ≤ 15%（单核计） |
| 控制端 CPU（解码 + 显示） | ≤ 10%（单核计） |
| 鼠标点击、键盘输入 | 两个方向都可用，常用键和中英文切换后的普通字符正确 |

四个方向组合都要跑：Mac 控 Mac（本机或两台）、Mac 控 Windows、Windows 控 Mac、Windows 控 Windows（如有第二台 Windows，否则跳过）。

达不到标准时，报告要指出慢在哪一段（采集、编码、网络、解码、上屏），作为是否继续的依据。

## 3. 不做什么

- 不做识别码服务器、公网打洞和中继：只在局域网内用 IP 直连。
- 不做账号、设备列表、文件传输、剪贴板、声音、聊天、多显示器（只采主屏）。
- 不做 Windows 系统服务、登录界面和 UAC 弹窗控制，不做 macOS 锁屏状态下的控制。
- 不捕获本机系统快捷键（Cmd+Tab、Win 键等）。
- 不做手机端。
- 界面只做验证需要的最小部分（见第 7 节），完整界面放到后面。

## 4. 方案取舍

### 4.1 画面显示（已定：原生视频视图）

| 方案 | 结论 |
|---|---|
| 原生视频视图：macOS `AVSampleBufferDisplayLayer`，Windows 硬解后送 DirectComposition 交换链，嵌在 Tango 的 `ui.NativeView` 位置 | **选用**。延迟和 CPU 最低，系统直接合成上屏。代价是原生视图盖在 Tango 画布上，悬浮工具条以后要做成跟随主窗口的子窗口；验证原型里工具条放在画面上方一条。 |
| Tango 渲染器加 YUV 视频纹理 | 不选。接近但多一次合成，看视频时整窗按 60 帧重绘；渲染器改动深。 |
| 解码成 RGBA 用 `ui.Image` 显示 | 不选。1080p60 每秒约 500 MB 拷贝。 |

### 4.2 传输

| 方案 | 结论 |
|---|---|
| WebRTC（`github.com/pion/webrtc/v4`，纯 Go） | **选用**。现成的 ICE、DTLS/SRTP 加密、拥塞控制、丢包重传和关键帧请求；以后做打洞和中继直接复用 STUN/TURN。本机模块缓存已有 v4.2.11。 |
| 自定义 UDP/QUIC 协议（RustDesk 的做法） | 不选。要自己做加密、拥塞控制和打洞，工作量大。 |

### 4.3 编码

| 方案 | 结论 |
|---|---|
| H.264 硬件编解码：macOS VideoToolbox，Windows Media Foundation | **选用**。两平台都有硬件支持，低延迟配置成熟，WebRTC 原生支持 H.264 打包。 |
| VP8/VP9/AV1 软件编码 | 不选。CPU 占用高。没有硬件编码器的机器以后再加 openh264 兜底。 |

## 5. 架构

一个可执行文件，同时包含被控端和控制端角色。

```
被控端                                              控制端
采集 → 编码(H.264) → pion 视频轨道 ──RTP/SRTP──→ pion 接收 → 解码+上屏（原生视图）
注入 ← 输入解码 ← pion 数据通道 ←──DataChannel──── 输入采集（Tango NativeView 区域）
        信令：控制端用 TCP 连接被控端 IP:端口，交换 SDP 和临时密码校验
```

### 5.1 目录结构

```
mmdesk/
  cmd/mmdesk/            程序入口（Tango 应用）
  internal/capture/      屏幕采集：capture_darwin.go(ScreenCaptureKit) / capture_windows.go(DXGI Desktop Duplication)
  internal/encode/       编码：encode_darwin.go(VideoToolbox) / encode_windows.go(Media Foundation)
  internal/display/      解码与上屏：display_darwin.go(AVSampleBufferDisplayLayer) / display_windows.go(MF 解码 + DirectComposition)
  internal/inject/       输入注入：inject_darwin.go(CGEventPost) / inject_windows.go(SendInput)
  internal/input/        输入事件格式与键码映射（平台无关，可单测）
  internal/session/      pion 连接、信令、轨道和数据通道、统计
  internal/stats/        时钟同步、分段延迟、帧率和 CPU 采样
  internal/ui/           验证用界面
  docs/
```

采集、编码、显示、注入都用 cgo 调系统框架（Objective-C / C）。Windows 版在 Mac 上用 `x86_64-w64-mingw32-gcc` 交叉编译（MinCode 打包已在用这条工具链）。

### 5.2 数据流细节

- **采集**：macOS 用 ScreenCaptureKit 输出 NV12 的 `CVPixelBuffer`，直接交给 VideoToolbox，不经过 CPU 拷贝。Windows 用 DXGI Desktop Duplication 拿到 D3D11 纹理，经视频处理器转 NV12 后交给 Media Foundation 编码器，全程在 GPU。画面不变时不出新帧。
- **编码**：低延迟实时模式，无 B 帧，固定 GOP 较长（例如 2 秒），收到对端关键帧请求（PLI）立即出 IDR，码率按 pion 拥塞控制估算动态调整，起步 8 Mbps。
- **传输**：视频走 H.264 RTP 轨道；每帧带采集时间戳（RTP 头扩展 abs-capture-time）。输入事件走两条数据通道：鼠标移动用无序、不重传的通道，点击和按键用有序、可靠的通道。
- **解码上屏**：macOS 把收到的 H.264 打包成 `CMSampleBuffer` 直接交给 `AVSampleBufferDisplayLayer`（系统内部硬解并上屏），按到达时间立即显示，不排队。Windows 用 Media Foundation 硬件解码器输出 D3D11 纹理，呈现到子 HWND 上的 DirectComposition 交换链。
- **输入**：控制端的原生视频视图不接收鼠标键盘（macOS 视图 `hitTest:` 返回 nil，Windows 子窗口返回 `HTTRANSPARENT`），事件落到下面 Tango 的 `NativeView` 区域，由 Tango 的输入系统读取，坐标按画面区域归一化到 0–1，键盘按物理键位（USB HID 用法码）发送；被控端映射回本机坐标和键码再注入。macOS 注入需要辅助功能权限，采集需要屏幕录制权限，首次启动引导授权。
- **信令和校验**：被控端监听一个 TCP 端口，界面上显示本机 IP 和 6 位临时密码；控制端输入 IP 和密码后连接，双方用密码做挑战应答校验，再交换 SDP。局域网内只用 host 候选地址。

## 6. 需要给 Tango 加的公开接口

Tango 的 `ui.NativeView` 已能在自绘界面里给原生视图留位置，内嵌 WebView 就靠它，但对接逻辑目前是 WebView 专用的内部接口。需要新增一个公开接口，让应用把自己的原生视图挂到这个位置上。设想的形状（名字和细节在 Tango 侧按其设计基线再定）：

```go
// uihost 包
type NativeHostOptions struct {
    // Create 在 UI 线程调用，parent 是窗口的原生内容视图（NSView* / HWND），
    // 返回应用创建的子视图句柄。
    Create  func(parent uintptr) (child uintptr, err error)
    Destroy func(child uintptr)
    // Covered 决定被弹层覆盖时的处理；视频视图用隐藏，不做快照。
    Covered CoverMode
}
func NativeHost(c *ui.Context, key string, opts NativeHostOptions, lopts ...layout.Option) *NativeHostW
```

uihost 每帧按 `NativeRegion` 设置子视图的位置、大小和可见裁剪，复用现有 WebView 内嵌的放置路径。改动遵守 Tango 规则：更新 `api/go/*.txt`、gofmt、`GOOS=linux/windows go vet`，提交作者 `tango <903300326@qq.com>`、不加 Claude 署名，在独立 worktree 里做，推送前问用户。

## 7. 验证用界面

用 Tango 自绘界面做三个窗口，按界面原型的风格，只保留验证需要的元素：

1. **主窗口**：左侧显示本机 IP、临时密码（可刷新）、允许被控开关；右侧输入对方 IP 和密码、连接按钮。
2. **会话窗口**：上方一条工具条（设备名、延迟、帧率、分辨率、码率、断开），下面是原生视频视图。
3. **被控提示**：有人连入时显示确认，连接期间右下角小面板可断开。

## 8. 测量方法

- **时钟同步**：连接建立后通过数据通道做 NTP 式往返测量，估算两端时钟偏差，之后每 10 秒校正一次。
- **分段延迟**：被控端记录采集完成、编码完成、发出的时间；控制端记录收齐一帧、交给显示层的时间（macOS 用显示层的呈现时间回调，Windows 用交换链呈现统计）。换算到同一时钟后得到每段耗时。
- **人眼校验**：被控端运行一个显示毫秒计时器的测试页，两台屏幕同框拍照，抽样核对端到端延迟，防止测量代码本身有偏差。
- **帧率、码率**：pion 统计接口加自己的帧计数。
- **CPU**：两端进程每秒采样，取画面持续变化（播放视频或滚动长页面）期间 60 秒的平均值。
- 结果写进 `docs/spike-report.md`，原始数据归档到 `/Users/admin/min/outputs/mmdesk-spike/`。

## 9. 推进顺序

1. **Mac 自己连自己**：本机同时开被控端和控制端，跑通采集 → 编码 → pion → 显示，以及输入注入。同时完成 Tango 原生视图接口。
2. **Windows 被控**：Windows 采集、编码、注入；Mac 控 Windows。
3. **Windows 控制**：Windows 解码上屏和输入采集；Windows 控 Mac。
4. **测量和报告**：四个方向跑完指标，写报告，给出是否继续和下一步建议。

## 10. 风险

- ScreenCaptureKit 和 Media Foundation 的 cgo 封装工作量可能超出预期，尤其是 Windows 的 D3D11/MF 互操作。
- Tango 自绘界面能否拿到物理键位、能否在原生视图之上正确接收鼠标事件，需要在第 1 步尽早验证。
- macOS 的屏幕录制和辅助功能权限每次重新编译未签名程序都可能失效，测试时需要重新授权。
- Windows 测试依赖用户的 Windows 电脑（见第 11 节）。

## 11. 待确认

- Windows 电脑如何部署和测试：如果开了 SSH，由我交叉编译后拷过去运行；否则由用户手动运行并反馈。
