# 隐私屏与锁屏实施计划

**目标**　在桌面控制端提供隐私屏、本次断开后锁屏、锁屏并断开，由被控端管理权限、系统状态和会话结束顺序。

**设计**　[隐私屏与锁屏设计](../specs/2026-11-26-privacy-screen-and-lock-design.md)

**实现方式**　在当前会话逐项实施。用 Go 管理会话和状态，经 cgo 调用 AppKit、ScreenCaptureKit 和 Win32。沿用 Tango 的设置页、会话面板和控制中心。

## 每项执行方式

1. 先添加该项的行为测试，运行并确认目标行为缺失导致失败。
2. 实现后重跑该项测试，再运行下列检查。Windows vet 同时检查 cgo 平台代码编译。锁屏测试使用假系统后端。
3. 独立提交。英文提交信息带派单指定的 Co-Authored-By 尾注。所有实现完成后重跑检查并做可安全执行的后台 GUI 验证。

```sh
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/app ./internal/session ./internal/privacy
GOOS=linux CGO_ENABLED=0 go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go vet ./...
```

新增包创建前，race 检查仅包含已存在的包。macOS 与 Windows 允许仅查看会话使用三个入口。设计第 7 节是验收要求。分支为 `feat/privacy-lock`。

## 1. 被控端状态和平台接口

- [ ] 在 `internal/app/host_privacy_test.go` 测试三档策略、请求关、30 秒超时、本地退出后重新询问、待确认和已开启时改为不允许、遮罩创建失败和重复请求。
- [ ] 在 `internal/app/host_privacy.go` 实现会话状态。用可替换的时钟和平台接口，串行处理开关、确认、平台事件和清理。确认绑定当前请求，过期回复无效。
- [ ] 在 `internal/privacy/privacy.go` 定义遮罩开关和锁屏接口以及不支持的平台实现。在 `internal/config/config.go` 加入隐私策略和自动锁设置，旧配置默认每次询问和关闭自动锁。

## 2. Meta 协议与服务端和控制端接线

- [ ] 在 `internal/session/privacy_test.go` 运行真实回环，覆盖所有双向消息、认证前丢弃、放行前丢弃、错误方向和畸形消息。验证空原因仍能识别主动 bye。
- [ ] 在 `internal/session/privacy.go` 定义严格校验的二进制消息。更新 `peer.go` 以接入已有认证闸门和记录是否收到 bye。文件会话放行后对三类请求给出不支持状态。
- [ ] 更新 `internal/app/host.go` 和 `viewer.go`。在被控端通过认证和连接确认后接收请求，在控制端以收到的回报更新勾选和提示。测试文件会话和拒绝时勾选恢复。

## 3. macOS 遮罩和采集过滤

- [ ] 测试遮罩属性、窗口排除更新和采集重启继续排除。用可控的截图测试比较遮罩上方的本地画面和排除遮罩后的画面。
- [ ] 在 `internal/privacy/privacy_darwin.{go,m,h}` 创建不激活、忽略鼠标、适配所有显示器和桌面空间的 AppKit 窗口，注册 ⌃⌥⌘P，所有窗口操作派到主线程。显示器变化重建遮罩时先取得并应用新窗口排除，再提高不透明度。
- [ ] 在 `internal/capture/capture_darwin.{go,m,h}` 更新现有 SCStream 过滤器并保存窗口号用于重启。保留本机自连的应用排除和主显示器选择。关闭或更新失败时撤掉遮罩并报告状态。

## 4. Windows 遮罩

- [ ] 测试低于 build 19041 或显示亲和性调用失败时，窗口保持隐藏且结果为不支持。Windows 真机用 DXGI 验证持续抓帧和鼠标穿透。
- [ ] 在 `internal/privacy/privacy_windows.{go,cpp,h}` 用 Win32 消息线程处理所有显示器的遮罩、Ctrl+Alt+Shift+P、显示器变化和置顶维护。在显示前设置 WDA_EXCLUDEFROMCAPTURE 和 alpha 254。保留现有 DXGI 采集路径。

## 5. 系统锁屏确认

- [ ] 在 `internal/privacy/privacy_test.go` 测试调用失败、系统确认和 5 秒超时。在 app 测试确认锁屏并断开只有收到成功才关闭连接，失败继续会话。
- [ ] macOS 加载 login.framework 的 SACLockScreenImmediate 并轮询当前会话已锁状态。Windows 调用 LockWorkStation 并以 WTS 会话状态确认。平台操作返回具体错误，断开原因用“对方已锁屏”。

## 6. 断开顺序、宽限期和应用退出

- [ ] 在 `internal/app/host_privacy_test.go` 用可控时钟覆盖主动结束立即锁、意外断开 60 秒后先锁后撤、新会话放行取消、本地快捷键取消、宽限期内正常退出立即锁、锁屏报错仍撤及已锁跳过。
- [ ] 在 `internal/app/host.go` 接线。在断网后释放连接和采集资源，由 Host 保留宽限期和遮罩。新会话通过确认后撤除旧遮罩。主动断开在流和会话清理前完成锁屏及撤罩。
- [ ] 在 `main.go` 为退出请求、最后一个窗口关闭、更新重启和 SIGTERM 保留事件循环直到 Host.Close 完成。更新重启利用 updater.EventReady 的同步回调。退出等待在事件循环之外执行。

## 7. 被控端设置、确认和会话面板

- [ ] 在 `internal/ui` 用现有测试工具检查设置默认值与变更、确认允许和拒绝、会话状态和宽限期倒计时。
- [ ] 更新 `internal/ui/settings_page.go`、`host_overlay.go` 和 `main.go`。安全设置即时应用并写入配置，会话面板显示隐私屏和锁屏状态，确认样式沿用连接确认弹窗。

## 8. 控制中心

- [ ] 测试普通和仅查看会话显示三个入口，传文件会话没有入口。断开后锁屏和隐私屏的勾选由被控端回报驱动。
- [ ] 更新 `internal/ui/session_window.go` 接入三项菜单和状态提示。隐私请求等待确认时显示“等待对方同意…”，失败显示原因。

## 9. 用户文档与验收

- [ ] 对照应用设置和菜单检查 `docs/guide` 与发版说明的文案、快捷键、版本和限制。文档项完成后运行同一套项目检查。
- [ ] 后台核对 macOS 遮罩排除、鼠标穿透、后方视频持续刷新，并记录 ⌘Tab、通知和 Spotlight 的验证情况。最后在可安全执行时手动锁屏一次。记录 Windows 真机待验项目。

### 真机验证记录

- macOS 26.6 后台测试用原生 AppKit 遮罩覆盖一个红色测试窗口。ScreenCaptureKit 截图在过滤前看到黑色遮罩，排除遮罩窗口后看到原有红色。确认遮罩忽略鼠标事件、不能接收键盘焦点，窗口层级为 1000，透明度为 254/255。
- 后台 WebKit 视频测试运行时，当前登录会话已锁定，ScreenCaptureKit 未返回显示器。全屏场景下的视频持续刷新、实际点击和快捷键、⌘Tab、通知及 Spotlight 遮盖仍待验证。
- 已确认 macOS 可以解析锁屏符号和读取当前登录会话的已锁定标志。主动锁屏及两种断开锁屏流程待在可现场解锁的环境验证。
- Windows 通过 mingw vet 和隐私包测试程序链接。待真机验证 DXGI 排除、浏览器视频刷新、键鼠穿透、多屏与热插拔、全局快捷键、版本门槛以及两种断开锁屏流程。
