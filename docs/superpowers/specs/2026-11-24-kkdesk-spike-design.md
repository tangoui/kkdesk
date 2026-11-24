# kkdesk 第一步：验证原型设计

日期：2026-11-24
状态：已确认（2026-11-24：画面用原生视频视图；Mac 与 Windows 相互控制；Windows 由用户手动运行；先扩展 Tango 输入接口）

## 1. 目的

kkdesk 是一个用 Tango 开发的远程桌面客户端（参考 RustDesk、ToDesk、UU 远程的产品形态，只借思路，不使用其代码；RustDesk 为 AGPL-3.0）。界面原型见 `/Users/admin/min/outputs/remote-desktop-ui/index.html`。

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

口径说明（以下为实现方的决定，标注〔自定〕）：

- 〔自定〕**延迟口径**：程序测量从被控端采集回调交出帧开始，到控制端显示层呈现为止，作为主指标。人眼拍照测的是屏幕到屏幕，比程序口径多一到两个刷新周期（约 15–30 ms），单独列出作参考，不用它判定达标。
- 〔自定〕**分辨率**：高分辨率屏（如 Mac 的 Retina）采集时由系统缩放到宽不超过 1920 再编码，指标按缩放后的 1080p 级别判定；另外跑一组原生分辨率作参考。
- 〔自定〕**CPU 口径**：同时报告两个数字：只算 kkdesk 进程；以及加上系统代做的部分（macOS 的 VTDecoderXPCService、WindowServer 增量，Windows 的 dwm 增量）。达标按后者判定。

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
| 原生视频视图：macOS `AVSampleBufferDisplayLayer`，Windows 硬解后送 DirectComposition 交换链，嵌在 Tango 的 `ui.NativeView` 位置 | **选用**。延迟和 CPU 最低，系统直接合成上屏。原生视图盖在 Tango 画布上，所以画面上不放任何控件：会话设置全部收进标签栏右侧的「控制中心」按钮（参考 UU 远程），控制中心用 Tango 的 `sys/menu` 弹出系统原生菜单（`menu.Popup`，支持二级菜单和勾选），系统菜单浮在所有视图之上，展开时视频照常播放，不需要隐藏或冻结。 |
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
kkdesk/
  cmd/kkdesk/            程序入口（Tango 应用）
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
- **编码**：低延迟实时模式，无 B 帧，固定 GOP 较长（例如 2 秒），收到对端关键帧请求（PLI）立即出 IDR。
- **画质档位**（第一步可用）：码率「自动 / 30 / 14 / 8 / 3 Mbps」，自动档按 pion 带宽估算动态调整，起步 8 Mbps；帧率「30 / 60 / 90 / 144」，不超过被控端屏幕刷新率。真彩（4:4:4）、HDR、中继连接在菜单里显示但不可选：4:4:4 和 HDR 依赖特定硬件，中继要等服务器。
- **传输**：视频走 H.264 RTP 轨道；每帧带采集时间戳（RTP 头扩展 abs-capture-time）。输入事件走两条数据通道：鼠标移动用无序、不重传的通道，点击和按键用有序、可靠的通道。
- **解码上屏**：macOS 用 VideoToolbox 解码会话（`VTDecompressionSession`）硬解出 `CVPixelBuffer`，包成标记「立即显示」的 `CMSampleBuffer` 交给 `AVSampleBufferDisplayLayer`，不排队。自己解码而不是把压缩帧直接交给显示层，是为了拿到准确的解码完成时间，也便于丢弃过期帧。Windows 用 Media Foundation 硬件解码器输出 D3D11 纹理，呈现到子 HWND 上的 DirectComposition 交换链。
- **输入**：控制端的原生视频视图不接收鼠标键盘（macOS 视图 `hitTest:` 返回 nil，Windows 子窗口返回 `HTTRANSPARENT`），事件落到下面 Tango 的 `NativeView` 区域，由 Tango 的输入系统读取，坐标按画面区域归一化到 0–1，键盘按物理键位（USB HID 用法码）发送；被控端映射回本机坐标和键码再注入。macOS 注入需要辅助功能权限，采集需要屏幕录制权限，首次启动引导授权。
- **信令和校验**：被控端监听一个 TCP 端口，界面上显示本机 IP 和 6 位临时密码；控制端输入 IP 和密码后连接，双方用密码做挑战应答校验，再交换 SDP。局域网内只用 host 候选地址。

## 6. 需要给 Tango 加的公开接口

共两处，都在 Tango 独立 worktree 里做，**第一步最先做输入扩展**，再做原生视图接口。

### 6.1 键盘鼠标输入扩展（最先做）

现状：`input.KeyEvent` 只有 `Key` 和 `Code` 两个固定枚举，拿不到原始键码，缺 F13 以上、PrintScreen、ScrollLock、Pause 和 ISO/JIS 键盘的额外键；`input.Pointer` 只有窗口内坐标，没有相对位移，鼠标移动按帧汇总后交给页面。

扩展内容（名字和细节在 Tango 侧按其设计基线再定）：

- `KeyEvent` 增加平台无关的物理键码（USB HID 用法码，覆盖全键盘）和平台原始键码（macOS virtual keycode / Windows 扫描码）。macOS 和 Windows 后端必须填写，Linux 后端能填则填；未知时为 0，现有代码不受影响。
- 新增一个可选的「原始指针流」：页面在某个区域（例如远程画面）订阅后，收到不按帧合并的每一个指针移动事件（带时间戳）；不订阅的页面行为不变。鼠标锁定和相对位移（游戏场景）不在这次范围内。
- 不包含：捕获本机系统快捷键（Cmd+Tab、Win 键），这需要底层钩子，放到后面。

### 6.2 原生视图接口

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
2. **会话窗口**：上方标签栏，右侧是延迟/帧率显示和「控制中心」按钮（弹出系统原生菜单：画质档位、屏幕、全屏、退出远控），下面整块是原生视频视图。
3. **被控提示**：有人连入时显示确认，连接期间右下角小面板可断开。

## 8. 测量方法

- **时钟同步**：连接建立后通过数据通道做 NTP 式往返测量，估算两端时钟偏差，之后每 10 秒校正一次。
- **分段延迟**：被控端记录采集完成、编码完成、发出的时间；控制端记录收齐一帧、解码完成、交给显示层的时间。上屏时间：macOS 的显示层没有逐帧呈现回调，取交给显示层之后的下一个屏幕刷新时刻（用 `CVDisplayLink` 获得）；Windows 用交换链呈现统计（`GetFrameStatistics`）。换算到同一时钟后得到每段耗时。
- **人眼校验**：被控端运行一个显示毫秒计时器的测试页，两台屏幕同框拍照，抽样核对端到端延迟，防止测量代码本身有偏差。
- **帧率、码率**：pion 统计接口加自己的帧计数。
- **CPU**：两端进程每秒采样，取画面持续变化（播放视频或滚动长页面）期间 60 秒的平均值。
- 结果写进 `docs/spike-report.md`，原始数据归档到 `/Users/admin/min/outputs/kkdesk-spike/`。

## 9. 推进顺序

1. **Tango 输入扩展**：先完成第 6.1 节，带测试。
2. **Mac 自己连自己**：本机同时开被控端和控制端，跑通采集 → 编码 → pion → 显示，以及输入注入。同时完成 Tango 原生视图接口（第 6.2 节）。
3. **Windows 被控**：Windows 采集、编码、注入；Mac 控 Windows。
4. **Windows 控制**：Windows 解码上屏和输入采集；Windows 控 Mac。
5. **测量和报告**：四个方向跑完指标，写报告，给出是否继续和下一步建议。

## 10. 风险

- ScreenCaptureKit 和 Media Foundation 的 cgo 封装工作量可能超出预期，尤其是 Windows 的 D3D11/MF 互操作。
- Tango 输入扩展要改 macOS、Windows 两个平台后端的事件转换，并保持现有页面行为不变；Tango 同时有其他会话在开发，worktree 合入时可能要处理冲突。
- 高帧率档位（90/144）要被控端屏幕支持对应刷新率；本机 Mac mini 接的是 4K 显示器，可能只能测到 60 帧，高帧率留待有高刷屏时再测，报告里注明。
- macOS 的屏幕录制和辅助功能权限每次重新编译未签名程序都可能失效，测试时需要重新授权。
- Windows 测试依赖用户手动运行（见第 12 节），每轮调试往返较慢；Windows 端要尽量把问题写进日志，减少来回。

## 11. 实现方的其他决定

- 〔自定〕Tango 接口推送前，kkdesk 用 `go.work` 指向本地 Tango worktree 开发，两边并行推进；Tango 改动是否合入和推送由用户决定。
- 〔自定〕控制端会话窗口获得焦点时，键盘一律按物理键位发送，中文由被控端的输入法合成；控制端本机输入法在会话中不参与。

## 12. Windows 测试方式（用户已定）

由用户手动运行。每轮我交叉编译出 `kkdesk.exe` 放到 `/Users/admin/min/outputs/kkdesk-spike/win/<日期-序号>/`，附操作步骤和要回传的内容（程序自动写的测量日志文件、现象描述或截图）。程序启动时把显卡型号、驱动、系统版本和所选编码器写进日志，用户不必另外提供。Windows 编码器按「硬件 Media Foundation H.264 → 软件 Media Foundation H.264」顺序选择，并在日志里记录。
