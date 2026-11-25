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
