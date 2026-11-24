# mmdesk 第 3 步：Windows 被控（Mac 控 Windows）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `mmdesk.exe -host` 在用户的 Windows 电脑上共享屏幕，Mac 上 `mmdesk -connect <Windows IP>` 看到并控制它；记录跨机器的分段延迟、帧率、CPU。

**Architecture:** 沿用第 2 步的平台无关接口（`internal/capture`、`internal/codec`、`internal/inject` 的 `Start`/`NewEncoder`/`New`），新增 `_windows` 实现，用 cgo + mingw-w64 调 DXGI / D3D11 / Media Foundation / SendInput。Windows 上的帧在 GPU 里流转：Desktop Duplication 得到 BGRA 纹理 → D3D11 视频处理器转 NV12 → MF 硬件 H.264 编码器（通过 `IMFDXGIDeviceManager` 共享同一 D3D11 设备）。跨机器延迟靠数据通道上的时钟同步。Windows 不能在本机运行，所以每个组件都要有自检命令 `mmdesk.exe -selftest`，用户运行一次就能把诊断日志带回来。

**Tech Stack:** Go 1.27、cgo（C/C++，`x86_64-w64-mingw32-gcc` 16，mingw-w64 14 头文件）、DXGI 1.2 Desktop Duplication、D3D11 Video Processor、Media Foundation（H.264 MFT，硬件优先，软件兜底）、`SendInput`；pion WebRTC（已有）。

**Spec:** `docs/superpowers/specs/2026-11-24-mmdesk-spike-design.md`（第 5、8、12 节，推进顺序第 3 项）；第 2 步计划 `docs/superpowers/plans/2026-11-24-mac-loopback.md` 的 Global Constraints 继续适用。

## Global Constraints

- 只支持 Windows 10 1809+ / Windows 11，x64（`GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++`）。arm64 Windows 不在本步范围。
- 交叉编译出的 exe 不依赖 mingw 运行库 DLL：静态链接 libgcc/libstdc++（`-static-libgcc -static-libstdc++` 或 `-static`），只依赖系统 DLL（d3d11、dxgi、mfplat、mf、mfuuid 为静态库、user32 等）。
- 采集：主显示器，DXGI Output Duplication；`AcquireNextFrame` 超时 ≤ 一帧时长；`DXGI_ERROR_ACCESS_LOST`（分辨率变化、UAC 安全桌面、锁屏）时重建并在日志记录；画面不变时不出新帧但每 1 秒至少补发一帧（防止新连接看不到画面）。缩放到 ≤ 1920 宽由视频处理器完成。鼠标指针：第一步不合成指针（控制端看到自己的指针），日志注明。
- 编码：H.264，MF 硬件编码器优先（`MFTEnumEx` + `MFT_ENUM_FLAG_HARDWARE|MFT_ENUM_FLAG_SORTANDFILTER`，异步 MFT 用事件模型），失败回退软件 `CLSID_CMSH264EncoderMFT`（需要时从 GPU 拷回 CPU 的 NV12）。属性：`CODECAPI_AVLowLatencyMode=TRUE`、`CODECAPI_AVEncCommonRateControlMode=CBR`、`CODECAPI_AVEncCommonMeanBitRate`、`CODECAPI_AVEncMPVGOPSize=fps*2`、`CODECAPI_AVEncMPVDefaultBPictureCount=0`、Baseline/ConstrainedBaseline 或 Main，强制关键帧 `CODECAPI_AVEncVideoForceKeyFrame`。输出转成 Annex-B，关键帧前带 SPS/PPS（从 `MF_MT_MPEG_SEQUENCE_HEADER` 或码流中取）。颜色 BT.709 limited。
- 注入：`SendInput`；鼠标 `MOUSEEVENTF_ABSOLUTE|MOUSEEVENTF_VIRTUALDESK` 映射到主显示器；键盘用 HID → Set 1 扫描码（`KEYEVENTF_SCANCODE`，扩展键加 `KEYEVENTF_EXTENDEDKEY`），表取 Tango `internal/platform/windows/canvas_keys.go` 中 `winScanHID`/`winExtScanHID` 的反向；滚轮 `MOUSEEVENTF_WHEEL/HWHEEL`（逻辑像素转 WHEEL_DELTA 比例，可调）。以普通权限运行，无法控制管理员窗口和 UAC 安全桌面（日志说明）。
- 时钟同步：控制端每秒通过可靠数据通道发 `ping{t0}`，被控端回 `pong{t0, t1}`（t1 为被控端收到时的本地时间），控制端在 t2 收到后估计 `offset = t1 - (t0 + t2)/2`、`rtt = t2 - t0`，取最近 10 次中 rtt 最小的样本的 offset；延迟计算把被控端的 CaptureAt 换算到控制端时钟。日志记录 offset、rtt。
- 诊断：`mmdesk.exe -selftest`：打印系统版本、每块显卡名称与驱动版本、主显示器分辨率和刷新率、采集 3 秒（帧数、尺寸、AcquireNextFrame 耗时）、编码器选择（硬件/软件，MFT 名称）、编码 60 帧（每帧耗时、关键帧、码率）、写出 `selftest.h264`；全部输出同时写到 `mmdesk-selftest.log`。主机模式启动时也把这些系统信息写进日志。
- 交付：`scripts/build-windows.sh` 产出 `/Users/admin/min/outputs/mmdesk-spike/win/<YYYYMMDD-序号>/mmdesk.exe` 和同目录 `README.txt`（中文：运行步骤、防火墙提示、要发回的文件）。另在 mmdesk 仓库 `dist/` 不提交二进制。
- 不复制第三方远程桌面软件代码；只用系统 API。提交、GUI 核对等其他规则同第 2 步。

## Review Focus

1. Windows 锁屏、UAC 弹窗、切换用户、分辨率/缩放改变时，采集要能恢复或明确报错，不能卡死或崩溃。
2. 硬件编码器不可用或创建失败（无 GPU 驱动、远程桌面会话里）时回退软件编码，日志写清楚原因。
3. 多显示器、DPI 缩放 125%/150% 时，鼠标坐标仍然落在正确位置。
4. 被控端时钟与控制端相差很多（几秒）时，延迟统计仍正确；时钟同步样本不足时日志标注「未同步」，不输出错误的延迟。
5. exe 在一台干净的 Windows 上能直接运行（不缺 DLL），首次监听端口时弹 Windows 防火墙提示，README 说明要允许。

---

### Task W1: 跨机器时钟同步（纯 Go）

**Files:** `internal/session/clock.go`、`internal/session/clock_test.go`；`internal/stats`（控制端用 offset 换算 Capture）；`internal/app/viewer.go` 接线（最小改动）。

**Interfaces:** `type ClockSync struct{...}`；`func (p *Peer) ClockOffset() (offset, rtt time.Duration, ok bool)`（控制端，ok=false 表示样本不足）；被控端自动应答 ping。stats 新增字段 `clock_offset_ms`、`clock_rtt_ms`、`clock_synced`。

- [ ] 测试先行：估计器在人为偏移（+3 s、−250 ms）和不对称延迟下误差 ≤ rtt/2；取最小 rtt 样本；Peer 往返测试在 127.0.0.1 上 offset≈0、ok 在 3 次 ping 内为 true。
- [ ] 实现 → `go test -race ./internal/session/ ./internal/stats/` → 提交。

### Task W2: Windows 采集与编码（cgo，Windows）

**Files:** `internal/capture/capture_windows.go` + `capture_windows.cpp/.h`；`internal/codec/encode_windows.go` + `encode_windows.cpp/.h`；共享 D3D11 设备 `internal/winmedia/`（设备、视频处理器、日志）；`internal/codec/decode_windows.go` 本步可仍为 unsupported。

**Interfaces:** 与 darwin 相同的 `capture.Start/Frame/Session`、`codec.NewEncoder/Encoder`；Windows 上 `Frame.Pixels` 是 NV12 `ID3D11Texture2D*`（同一 D3D11 设备，回调期间有效）。编码器 `Info()` 报告硬件/软件与 MFT 名称。

- [ ] 实现；交叉编译：`GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go build ./...` 与 `go vet` 通过；darwin 构建与测试不受影响。
- [ ] 纯逻辑部分（AVCC/MF 输出 → Annex-B、SPS/PPS 提取、缩放尺寸）放无构建标签文件并在 Mac 上单测。
- [ ] 提交（不能在本机运行，提交信息注明「未实机验证」）。

### Task W3: Windows 注入（cgo 或纯 syscall）

**Files:** `internal/inject/inject_windows.go`（可用 `golang.org/x/sys/windows` 或 syscall 直接调 `SendInput`，不需要 cgo 更好）；HID → 扫描码表 `internal/input/keymap_windows_table.go`（无构建标签）+ 测试（与 Tango 表互为反函数，表数据复制进测试）。

- [ ] 测试先行（表、坐标换算到 65535 绝对坐标、滚轮换算）→ 实现 → 交叉编译 vet → 提交。

### Task W4: Windows 主机接线、自检与打包

**Files:** `internal/app/host.go` 的平台相关部分拆到 `host_darwin.go`/`host_windows.go`（权限检查、排除自身窗口仅 darwin）；`internal/app/sysinfo_windows.go`（系统、显卡、显示器信息）；`selftest.go`（`-selftest` 标志）；`scripts/build-windows.sh`；README 模板。

- [ ] Windows 上 `-host` 显示主窗口（Tango 自绘在 Windows 可用），打印 `mmdesk host listening on … password …`；控制端仍是 Mac。
- [ ] `scripts/build-windows.sh` 产出 exe + README.txt 到 outputs/mmdesk-spike/win/<日期-序号>/；用 `x86_64-w64-mingw32-objdump -p mmdesk.exe | grep 'DLL Name'` 确认只依赖系统 DLL。
- [ ] 提交。

### Task W5: 用户实机测试与报告

- [ ] 用户在 Windows 上先跑 `mmdesk.exe -selftest`，发回 `mmdesk-selftest.log`；根据日志修问题、重新打包（序号递增）。
- [ ] 用户在 Windows 上跑 `mmdesk.exe -host -log host.jsonl`，在 Mac 上跑 `mmdesk -connect <IP>:21118 -password <密码> -log view.jsonl` 60 秒（播放视频或拖动窗口），然后试控制（打开控制模式，点击、打字、滚轮）；发回 `host.jsonl`、主机日志。
- [ ] 汇总写入 `docs/spike-report.md` 第二节。
