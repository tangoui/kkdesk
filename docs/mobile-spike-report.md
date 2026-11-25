# kkdesk 手机端第 0 步验证

测量日期 2026-11-25。被控端为 Mac mini M4、16 GB、macOS 26.6（4K 显示器，界面 1920×1080 @ 60 Hz）。手机端使用 Flutter 3.47.7 和 flutter_webrtc 1.6.3。iOS 设备为 iPhone 12 Pro（iOS 27.0.1），Android 使用 API 36 arm64 模拟器。

**第 0 步验证未通过，暂停进入第 1 期。** iPhone 与现有电脑端的 H.264 互通，触控成功。采集到手机解码交帧的中位延迟为 **353.1 ms**，已经超过 80 ms 停止线。

## iPhone 真机结果

| 指标 | 结果 |
|---|---|
| 采集 → iPhone 视频回调，P50 / P95 | 353.1 / 1438.8 ms，2015 帧，约 45 秒 |
| 剔除启动和停顿段，仅取 15:15:10–15:15:25 | P50 323.7 ms，874 帧 |
| 1080p H.264 解码帧率 | 上述 45 秒约 44.8 fps，上一轮 60 秒约 47.3 fps |
| iPhone 进程 CPU | 视频播放期间 31 个 DVT 样本中位数 27.4%，最高 46.0%（上一轮） |
| iPhone 热状态 | 样本全部为 `nominal`。真机接 USB，未单独测电池耗电或机身温度 |

首轮探针未保留独立的原生轨道对象，设备解码正常，但探针没有收到帧。保留轨道后，15:14:56–15:15:41 的测量收集到上述时间戳。探针样本所用的时钟同步 RTT 为 18.6–108.9 ms。最后一条配对样本附近的 libwebrtc 计数器显示 585 个 RTP 包丢失，累计抖动缓冲时间除以出缓冲帧数约为 205 ms/帧。测试期间 Mac 同时进行多项构建并出现内存压力和采集停顿。各环节对延迟超标的贡献仍待隔离复测。

## 测法

被控端运行现有 `kkdesk -host -autoaccept`，采集上限为 1920 像素宽、60 fps、自动码率 8 Mbps。桌面负载使用 `scripts/anim` 的 960×540 动画。测试窗口用 `TANGO_LAUNCH_BACKGROUND=1` 启动。

手机使用现有局域网 TCP 密码挑战和 WebRTC 非 trickle SDP 信令。接收端通过 `RTCVideoView` 显示视频，触控通过 `input` 和 `input-move` 数据通道发回现有桌面进程。时钟同步复用 `meta` 通道的 ping/pong，用最近 10 个往返样本中 RTT 最短的一条估计时钟偏移。iOS 测量模式将 Go 的采集时间和原生视频回调按 RTP 时间戳匹配。测点在解码器交帧处，到实际屏幕刷新还有一段时间。帧率取解码帧数的差值除以采样时间。CPU 用 iOS DVT sysmon 的进程采样，热状态用 `ProcessInfo.thermalState`。

```sh
go build -o /tmp/kkdesk-mobile-spike/kkdesk .
go build -o /tmp/kkdesk-mobile-spike/anim ./scripts/anim
TANGO_LAUNCH_BACKGROUND=1 /tmp/kkdesk-mobile-spike/anim -w 960 -h 540
TANGO_LAUNCH_BACKGROUND=1 KKDESK_CONFIG_DIR=/tmp/kkdesk-mobile-spike/config \
  /tmp/kkdesk-mobile-spike/kkdesk -host -autoaccept -listen :26118 \
  -password "$SPIKE_PASSWORD" -log /tmp/kkdesk-mobile-spike/desktop.jsonl

cd mobile
flutter build ios --release --dart-define=KKDESK_SPIKE_METRICS=true \
  --dart-define=KKDESK_SPIKE_ADDRESS="$SPIKE_ADDRESS" \
  --dart-define=KKDESK_SPIKE_PASSWORD="$SPIKE_PASSWORD"
flutter build apk --release --target-platform android-arm64 \
  --dart-define=KKDESK_SPIKE_METRICS=true \
  --dart-define=KKDESK_SPIKE_ADDRESS="$SPIKE_ADDRESS" \
  --dart-define=KKDESK_SPIKE_PASSWORD="$SPIKE_PASSWORD"
```

`KKDESK_SPIKE_ADDRESS` 包含端口。测量构建预填测试地址和口令并自动连接。iOS 构建实测使用从官方 SwiftPM 压缩包提取的同版本 iPhone arm64 二进制，以绕开包含多平台调试符号的完整包下载。临时覆盖位于测试目录。iOS 测量文件位于应用容器的 `Documents/kkdesk-spike.jsonl`，可用 `xcrun devicectl device copy from` 取回。Android 统计由 `adb logcat` 采集。

## 已验证

- iPhone 12 Pro 实际通过同一 Wi-Fi 用地址和密码连到 Mac。收到 1920×1080 H.264，显示了桌面画面。Mac 日志记录了鼠标按钮和移动事件已注入。
- iOS 接收侧报告解码器为 VideoToolbox，`powerEfficientDecoder` 为 `false`。硬件解码未得到证实。
- `go test ./...` 和 `GOOS=windows go vet ./...` 通过。对仓库 Go 文件运行 `gofmt -l` 无输出。`flutter analyze` 通过，`flutter test` 的 3 项测试全部通过。
- Android 模拟器已启动，并可读到测试端口上的密码挑战。首轮制品构建因 Maven 下载连接重置失败，重试后遇到 Gradle 资源目录写入冲突。Android App 的完整连接、画面、触控和性能验收仍待完成。
- 真机原始帧样本在本机 `/tmp/kkdesk-mobile-spike/iphone-gate.jsonl`。对应的被控端和 CPU 日志为 `desktop-verification.jsonl`、`desktop-verification.out` 和 `iphone-stream-cpu.jsonl`。

第 1 期的计划、功能实现、协议金样本和扫码跨网验收截图均未开始。

## 延迟专项复测

分支变基到 `origin/main` 的 `6a2d130` 后，重新构建 Mac 被控端与 iPhone 测量版。所有 iPhone 对照都使用 1920×1080、同一套 960×540 动画和解码交帧测点。每组单独启动被控端，保留约 60 秒日志。中间 Mac 的局域网地址曾变化，监听失败的那轮未计入。实测期间未运行本轮构建或 Android 模拟器，其他会话和系统进程仍在运行。多轮 `memory_pressure -Q` 的可用内存百分比有波动。

| iPhone 测试条件 | 匹配帧数 | P50 / P95（ms） | 抖动缓冲均值（ms/帧） | RTP 丢失包数 / NACK |
|---|---:|---:|---:|---:|
| 原版，60 fps，自动码率，第一轮 | 2,330 | 449.6 / 793.1 | 178.6 | 1,106 / 103 |
| 原版加零 playout-delay，60 fps，自动码率 | 4 | 样本不足 | 517.7 | 3,712 / 389 |
| 原版，60 fps，3 Mbps | 1,003 | 529.1 / 1,830.3 | 304.1 | 1,166 / 392 |
| 启用 RTX，60 fps，自动码率 | 1,520 | **304.0 / 1,116.3** | 115.0 | 2,203 / 307 |
| 原版，60 fps，自动码率，紧接 RTX 的对照 | 939 | 629.9 / 2,674.0 | 343.1 | 3,437 / 371 |
| 启用 RTX，30 fps，自动码率 | 872 | 859.9 / 3,746.7 | 303.8 | 1,938 / 247 |
| 启用 RTX，30 fps，3 Mbps | 232 | 1,338.7 / 2,249.7 | 471.6 | 866 / 216 |

- 第一轮被控端采集到编码完成 P50 为 15.4 ms，编码完成到发送 P50 为 0.1 ms。iPhone 解码平均 6.9 ms。上表抖动缓冲和解码时长由相同时间窗内的 libwebrtc 累计值差分计算。其余网络、接收排队和时钟偏移无法分别按帧定量。RTT 在 RTX 轮为 97.9–646.0 ms，在紧接的对照轮为 42.1–1,076.7 ms。链路波动限制了跨轮比较。
- Pion 已有 NACK responder，现有 PLI/FIR 处理会要求编码器在下一帧输出 IDR。此次补上 H.264 RTX 协商，两种接收端的 SDP 兼容性均有回归测试。iPhone 的 RTX 对照轮收到 1,300 个重传包，报告 7 个 PLI 和 0 个 FIR。保留 RTX，继续维持原版发送帧率和码率。
- 零 playout-delay 已通过本地 RTP 验证，但 iPhone 对照时严重丢包和停顿，未留下足够可比较的样本。最终提交撤下该实验。降码率和降帧率也未测出稳定收益。拥塞控制调参和 720p 未测试。
- iOS 接收端使用 `RTCVideoView`，逐帧探针直接挂在原生视频轨上，因此表中延迟不含 Flutter 纹理上屏。VideoToolbox 的统计仍将 `powerEfficientDecoder` 标为 `false`，实际硬件加速还需独立确认。
- Mac 自连采用相同的 45 秒脚本，改动前 P50 / P95 为 37.2 / 49.5 ms，改动后为 26.2 / 37.0 ms，均为零丢包。改动后也低于最初报告的 33.0 / 49.8 ms。

### Android 模拟器验收

旧 APK 在 libwebrtc 网络监视启动时因为缺少 `ACCESS_NETWORK_STATE` 崩溃。补上权限后重新构建并在 API 36 arm64 模拟器无窗口安装，验证地址密码连接、1080p H.264 画面和触控点击。Mac 日志确认按钮事件注入。Android 日志最后一秒报告 21 fps、0 丢包。两次 Android `top` 采样为 100% 和 117% 单核 CPU 口径，虚拟热状态为 0。模拟器未配备逐帧 RTP 交帧探针，其性能数据仅记录解码和缓冲统计。

### 复测命令和日志

沿用报告上面的构建和被控端命令，切换实验时分别追加 `-bitrate 3`、`-fps 30` 或两者。真机以 `xcrun devicectl device process launch --terminate-existing` 启动，用 `xcrun devicectl device copy from --domain-type appDataContainer` 导出原始帧日志。Mac 自连命令为 `ANIM_ARGS='-w 960 -h 540' PORT=26119 scripts/loopback.sh <日志目录> 45`。Android 用 `adb install -r`、`adb shell am start -W`、`adb exec-out screencap -p`、`adb shell input tap` 和 `adb logcat -d -s flutter:I` 验收。

原始 iPhone 和 Mac 日志位于本机 `/tmp/kkdesk-mobile-spike/latency-*`，Android 截图和启动、触控、帧率日志位于该目录的 `latency-android-final/`。Go 全量测试、Windows vet、Go 格式检查、Flutter 分析与 3 项测试均通过。

**1080p 真机延迟仍未达到 80 ms，中位数最好为 304.0 ms。第 1 期未开始。**
