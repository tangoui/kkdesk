# 架构

kkdesk 是一个 Go 程序，平台相关的部分用 cgo 写在各自的包里，接口与平台无关。界面用 [Tango](https://github.com/tangoui/tango) 自绘，远端画面是嵌进窗口的系统原生视频视图（`uihost.NativeHost`）。

## 两条流水线

```
被控端：屏幕采集 ──▶ H.264 编码 ──▶ session（WebRTC 视频）──────▶ 控制端
        输入注入 ◀── session（数据通道）◀── 键盘鼠标 ◀────────── 控制端

控制端：session（WebRTC 视频）──▶ H.264 解码 ──▶ 原生视频视图
        界面里的键盘鼠标 ──▶ session（数据通道）──▶ 被控端
```

## 包

| 包 | 职责 |
|---|---|
| `main`（根目录） | 解析命令行参数，读配置，创建被控端、主窗口或控制端。 |
| `internal/ui` | 界面：主窗口（连接、我的设备、远程会话、文件传输、设置）、会话窗口、文件窗口、连接确认和被控面板。 |
| `internal/app` | 把各部分接起来：`Host`（采集 → 编码 → 会话，会话输入 → 注入）和 `Viewer`（会话 → 解码 → 显示，界面输入 → 会话），以及在线探测、自检、诊断日志。 |
| `internal/session` | 信令和 WebRTC 会话（pion v4）：视频轨、数据通道、画质请求、时钟同步、剪贴板消息。 |
| `internal/rendezvous` | 跨网服务的信令客户端和服务端：设备码上线、配对、交换连接信息、签发 TURN 凭据。 |
| `internal/capture` | 采集主显示器为 NV12 帧。macOS：ScreenCaptureKit；Windows：DXGI Desktop Duplication。 |
| `internal/codec` | H.264 编解码。macOS：VideoToolbox 硬件编解码；Windows：Media Foundation 编码（优先硬件，回退软件），解码尚未实现。 |
| `internal/display` | macOS 原生视频视图（`AVSampleBufferDisplayLayer`），把视图里的指针位置换算成视频坐标。 |
| `internal/inject` | 执行对方的键盘鼠标操作。macOS：`CGEventPost`；Windows：`SendInput`。 |
| `internal/input` | 输入事件的二进制编码。 |
| `internal/winmedia` | Windows 采集和编码共用的 Direct3D 11 设备。 |
| `internal/files` | 文件传输协议的客户端和服务端。 |
| `internal/clipsync` | 剪贴板同步：轮询本机剪贴板、发送变化、写入收到的内容、防回弹。 |
| `internal/config` | 本机配置和通讯录（`kkdesk.json`），原子写入，损坏时备份并用默认值。 |
| `internal/stats` | 每帧时间戳、每秒汇总的 JSON Lines 统计和汇总工具。 |
| `cmd/kkdesk-server` | 自托管跨网服务：信令 + TURN。 |

## 局域网信令

被控端在 TCP 端口（默认 21118）上监听，每行一个 JSON：

```text
被控端 → 控制端  {"nonce": base64}
控制端 → 被控端  {"mac": base64(HMAC-SHA256(password, nonce))}
被控端 → 控制端  {"ok": true} | {"ok": false, "error": "bad password"}
控制端 → 被控端  {"sdp": offer, "mode": "desktop" | "files"}
被控端 → 控制端  {"sdp": answer}
```

- 密码不上网络，只传对随机数的 HMAC。被控端可同时接受多个密码（临时 + 固定）。
- offer 和 answer 都是收集完 ICE 候选后的完整 SDP。
- 「我的设备」的在线探测只连上 TCP 就断开，被控端把它当作探测忽略。

## 跨网信令

被控端用 WebSocket 连到 `kkdesk-server`，以设备码上线并保持连接；控制端用设备码请求配对，服务端转发 offer 和 answer，并给双方签发 TURN 临时凭据。之后的 WebRTC 连接与局域网相同，由 ICE 决定直连还是走 TURN 中继。

## WebRTC 会话

视频是 H.264 RTP（传文件模式没有视频轨）。数据通道：

| 通道 | 可靠性 | 内容 |
|---|---|---|
| `input-move` | 无序、不重传 | 鼠标移动：丢了就丢了，只要最新位置。 |
| `input` | 有序、可靠 | 点击、按键、滚轮。 |
| `meta` | 有序、可靠 | 控制消息，见下表。 |
| `files` | 有序、可靠 | 文件传输协议。 |

`meta` 通道的消息第一个字节是类型，多字节整数都是大端：

| 类型 | 方向 | 内容 |
|---|---|---|
| `c` | 被控 → 控制 | `u32 RTP 时间戳, i64 采集时刻`：用来算端到端延迟。 |
| `q` | 控制 → 被控 | `u32 码率, u16 帧率`：画质请求。 |
| `p` / `o` | 双向 | 时钟同步的 ping / pong，取往返最短的一次估算两边时钟差。 |
| `x` | 双向 | 剪贴板文字：`u32 文本 id, u8 是否最后一块, 内容`，每块 16 KiB，最多 1 MiB。 |
| `b` | 双向 | 结束会话及原因。 |

收到不认识的类型会直接忽略，所以新旧版本互连时新增的消息不会出错。

## 文件传输协议

所有消息走一条有序可靠的 `files` 通道：

- 控制消息是 JSON 文本：请求带 `id` 和 `op`（hello、home、list、stat、mkdir、download、upload_begin、end、cancel），回复 `{"id":n,"ok":true,...}` 或带错误码的失败回复。
- 文件内容是二进制消息：`[1 字节类型][8 字节传输 id][8 字节偏移][数据]`，每块最多 63 KiB，保持在 SCTP 消息上限以内。
- 每个文件以 `{"op":"end","sha256":...}` 结束，接收方校验大小和 SHA-256，再把临时文件 `.<名称>.kkdesk-part` 改成正式名字。
- 发送方按 `BufferedAmount` 做流量控制（高水位 4 MiB，低水位 1 MiB）。

## 延迟怎么测

被控端为每帧记下采集时刻，通过 `meta` 通道的 `c` 消息发给控制端；控制端在画面显示时记下时刻，用时钟同步得到的时差换算后相减，就是端到端延迟（采集 → 编码 → 传输 → 解码 → 显示）。统计每秒汇总一次，写进 `-log` 指定的 JSON Lines 文件。
