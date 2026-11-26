# 锁屏远控阶段 0 验证报告

日期：2026-11-26

状态：探测产物已构建，锁屏、管理员授权及 Windows 运行结果待补。

## 已确认

- macOS 26.6（25G72，arm64）可以运行用 macOS 27.2 SDK 构建的独立 `KKDeskLockSpike.app`。未锁屏时，普通用户进程的 launchd 上下文为 `Aqua`，Quartz 会话在控制台，主屏 ID 为 1。当前会话与 IOKit 控制台用户记录里的锁标记都缺失，程序会等到其中一个来源给出明确的锁定信号。
- 新 SDK 已把 `CGWindowListCreateImage` 和 `CGDisplayStream` 的创建 API 标为过时且禁止直接调用。当前系统仍能解析两个运行时符号。探测用运行时符号调用旧接口，同时用 ScreenCaptureKit 的 `SCScreenshotManager` 采单帧。结果只覆盖单帧能力，持续 `SCStream` 仍需单独验证。[CGWindowListCreateImage](https://developer.apple.com/documentation/coregraphics/cgwindowlistcreateimage) · [CGDisplayStream](https://developer.apple.com/documentation/coregraphics/cgdisplaystream) · [SCScreenshotManager](https://developer.apple.com/documentation/screencapturekit/scscreenshotmanager)
- macOS 13 起，`SMAppService` 支持从签名 app 的 `Contents/Library/LaunchDaemons` 和 `Contents/Library/LaunchAgents` 注册。守护进程受管理员批准约束。测试包用独立 bundle ID 和临时签名，并包含三个以 `BundleProgram` 指向同一可执行文件的 plist。严格签名检查通过。普通用户执行三种 `status` 查询均返回 `notFound (3)`。这是产物目录里的只读结果，注册响应和系统设置中的审批状态还待验证。[SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice) · [SMAppService 注册说明](https://developer.apple.com/documentation/servicemanagement/smappservice/register())
- 本机 `launchctl(1)` 将 Aqua 和 LoginWindow 列为不同会话类型，并注明 LoginWindow 代理以 root 运行。阶段 0 用程序自身的 `managername` 和审计会话信息判定运行身份。已登录用户的普通锁屏是否会启动这种代理，仍需实测。
- Microsoft 的 `DuplicateOutput` 文档说明安全桌面访问受权限限制，并明确举出 LocalSystem 可以访问安全桌面的例子。`SendInput` 受 UIPI 限制，返回的事件计数无法证明密码框收到字符。需要对照现场观察和截图。[DuplicateOutput](https://learn.microsoft.com/en-us/windows/win32/api/dxgi1_2/nf-dxgi1_2-idxgioutput1-duplicateoutput) · [SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput)
- Windows 探测 exe 已用 MinGW 和 cgo 交叉编译为 PE32+ x86-64，导入表只有系统 DLL，SHA-256 已核对。一次性 LocalSystem 服务通过 WTS 查询控制台锁定状态，通过会话绑定的 LocalSystem 令牌启动助手。助手检查 Winlogon 输入桌面并把 DXGI 和固定按键探测结果写入受保护目录。该流程的 Windows 实机结果待补。[WTSINFOEX_LEVEL1_W](https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/ns-wtsapi32-wtsinfoex_level1_w) · [CreateProcessAsUserW](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessasuserw) · [SetThreadDesktop](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setthreaddesktop)

## 实机待确认

| 平台 | 测量项 | 判定证据 |
| --- | --- | --- |
| macOS | 普通用户、Aqua、root LaunchDaemon、LoginWindow 是否启动并观察到锁定 | `launch.log` 及 `probe.log` 中的 uid、manager、审计会话、两个锁标记 |
| macOS | 三种截图 API 在锁屏身份、授权组合下能否采到登录界面 | 原始结果、PNG 尺寸、是否黑屏或透明、可见界面内容 |
| macOS | HID、Session、Annotated Session 三种 tap 是否进密码框 | 每轮单 tap，比较前后圆点数。没有可用 PNG 时记录现场观察 |
| macOS | SMAppService 三种作业的注册及注销流程，临时签名与发布签名的区别 | `status`、注册错误、系统设置对应条目及批准前后的变化 |
| Windows | LocalSystem 服务能否在活动控制台启动助手，助手能否进入 Winlogon 桌面 | `service.log` 和 `helper.log` 的会话号、桌面名、系统错误 |
| Windows | DXGI 是否出锁屏帧，SendInput 是否进入密码框，服务能否卸载 | 两张 PNG、DXGI HRESULT、输入计数及现场圆点、卸载后查询 |

Windows 的 Ctrl+Alt+Del、UAC 安全桌面、远控会话接续以及 macOS 连续画面流都需要独立的验证结果。本轮探测只使用固定字母，密码由操作人手动输入，不作为探测输入或日志字段。

## 初步推荐与设计调整候选

- **Windows**保留第 5.1 节 LocalSystem 服务与按会话划分的助手。Microsoft 文档为 DXGI 路径提供了依据。待实测通过后，再决定助手是否要长期跟踪输入桌面。阶段 0 使用的是单次 WTS 锁定检测和 Winlogon 桌面校验。
- **macOS**先以普通用户进程和 Aqua 作业建立基线，再验证 root LaunchDaemon 与 LoginWindow。LoginWindow 纳入第 5.1 节实现的前提是在已登录的锁屏过程中观测到它启动，并取得可用画面和输入。采集优先验证 ScreenCaptureKit，两个旧接口作为本轮对照。根据签名和批准实测再决定第 5.4 节的安装方式。
- **本机 IPC**保留第 5.2 节的同一用户与代码签名要求。Windows 应在正式实现前明确验证客户端映像的签名策略，因为路径比对只能确认路径。macOS 应验证实际连接对端的代码身份与当前控制台 UID，不能只验证本机磁盘上预期路径的 app。

## 构建与交付

源码位于 `cmd/lockspike/mac` 与 `cmd/lockspike/win`。各目录的 `build.sh` 默认写入 `/Users/admin/min/outputs/kkdesk-lockscreen-spike/mac/` 与 `/Users/admin/min/outputs/kkdesk-lockscreen-spike/win/`。两处产物均附中文 README。

本机通过 `gofmt -l .`（空输出）、`go vet ./...`、`go test -count=1 ./...`、`GOOS=linux CGO_ENABLED=0 go vet ./...` 和 Windows amd64 MinGW cgo `go vet ./...`。本机测试链接器提示重复的 `-lobjc`，退出状态为 0。
