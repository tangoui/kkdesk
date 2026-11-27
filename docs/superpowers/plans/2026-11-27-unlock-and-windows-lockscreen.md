# Windows 锁屏远程操作与控制端解锁实施计划

**目标**　在已获准的远控会话中，Windows 锁屏后继续提供画面和操作，控制端可主动提交登录密码或 PIN。

**架构**　Windows 服务和助手处理本机锁屏画面与输入。主进程负责会话认证、编码、网络及采集源切换。控制端凭据以经过验证的被控端公钥为索引。

**设计**　[本期范围](../specs/2026-11-27-unlock-and-windows-lockscreen-design.md)优先，补充采用[锁屏设计](../specs/2026-11-26-lockscreen-access-design.md)第 5 节 Windows 部分及第 7 节。Windows API 用法参照 `cmd/lockspike/win/`，实机验证安排在最终测试包交付后。

## 2026-11-27 补充决定

1. Windows 校验本机 IPC 客户端 SID 和当前控制台会话，保留锁屏设计第 5.2 节的受保护映像路径校验。记录每次连接的 PID、路径、SID 和结果。代码签名留待后续。只有 WTS 明确报告会话已锁定才启用助手。
2. 「仅查看」由控制端本地开关负责。隐藏解锁菜单并阻止控制端发送解锁请求。被控端仍要求认证和会话批准，拒绝 files-only。
3. Windows 完成后实现 macOS 被控端。macOS 按开发者 Team ID 验证 IPC 对端；当前发布流程为临时签名，实现 macOS 平台任务时核对 Team ID 来源。

## 全部任务适用

- 工作目录为 `.kkdesk-unlock`，分支为 `feat/lockscreen-unlock`。计划单独提交，之后每个任务先证实失败测试，再实现并单独提交。提交使用英文，并附 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。
- 服务默认不安装，被控端本机主动授权安装、升级和卸载。IPC 无网络监听，只对已验证的当前控制台用户及预期 kkdesk 程序开放。助手在锁屏且有活动桌面会话请求时处理画面和输入。未安装组件时报告当前平台的锁屏能力。
- 解锁只因控制端用户点击触发。成功以系统锁定状态为准，等待上限为 15 秒。每个会话最多调用助手尝试 3 次。凭据在 Windows 使用凭据管理器，在 macOS 使用钥匙串，Linux 仅提供手输。凭据使用可清理的字节缓冲，不进入日志、设置或诊断。
- 用既有依赖和平台原生 API。macOS 上只运行普通单测及交叉检查，不锁屏或提权。凭据集成测试使用独立 service 或目标前缀，提前注册清理，测试结束删除测试项。
- 每个任务后及最后运行下列命令，`-race` 一并覆盖当时已新增的包。现有的 `internal/input/proto_test.go` 和 `internal/session/auth_test.go` 由其他会话维护，保持原样。所有新文件和修改的 Go 文件都通过 gofmt 检查。
- 计划提交前，本机与两项跨平台 vet 均通过。全量测试中，`internal/app` 的锁屏测试提前返回屏幕录制权限错误，`internal/codec` 的 `TestEncodeDecode1080p` 报 `OSStatus -12908`，`internal/inject` 的 `TestNewReadsMainDisplay` 无法读取主显示器尺寸。race 检查中 `internal/session` 通过，`internal/app` 有相同的权限错误。任务 2 应让假采集测试注入权限检查，编解码及显示器测试应记录实跑结果。

```sh
gofmt -l .
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/app ./internal/session
GOOS=linux CGO_ENABLED=0 go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go vet ./...
```

## 任务 1　锁屏与解锁协议

- [ ] 在 `internal/session/lockscreen_test.go` 先写消息验证测试。断言认证未完成、还在等待本机确认和 files-only 都不能交付解锁请求。测试方向错误、非法 UTF-8、超长数据、重复注册及初始锁屏状态先于 viewer 注册。
- [ ] 运行 `go test ./internal/session -run 'Test(LockScreen|Unlock)'` 并检查失败。新增 `internal/session/lockscreen.go`，接入 `peer.go` 的有序 meta 通道。实现 `LockScreenState`、`UnlockResult`、状态订阅和接收、`SendUnlock(password []byte) error`，以及在 host 会话批准后注册的解锁接收器。缓存最近一次状态给稍后订阅的控制端，只在当前回调期间交付请求字节。
- [ ] 重跑目标测试和全部检查，提交 `feat(session): add lock screen and unlock messages`。

## 任务 2　被控端采集切换和解锁请求

- [ ] 在 `internal/app/host_locked_test.go` 及新增 `host_unlock_test.go` 注入假锁屏状态源、假助手及假采集器。覆盖连接时已锁、会话中锁屏、解锁后恢复正常帧和关键帧、助手断开、锁定期间会话关闭。断言正常采集与助手采集不会同时发布画面，锁定期间输入只到助手，并在切换和结束时清理按住的键。覆盖在确认之前和 files-only 时无助手调用。
- [ ] 追加用例覆盖未锁屏、组件不可用、成功、15 秒超时、连续 3 次与第 4 次请求、会话中断取消和日志不含测试密码。用可控计时器验证超时与断开，无需真实等待。运行 `go test ./internal/app -run 'Test(Lock|Unlock|CaptureFailure)'` 并检查失败。
- [ ] 在 `internal/app/host.go` 和新增 `host_lockscreen.go` 接入 `internal/lockscreen` 接口。平台驱动提供 `Start(context.Context, capture.Options, func(capture.Frame)) (Session, error)`，会话提供 `Close()`、`Apply(input.Event) error`、`Unlock(context.Context, []byte) error`，复用帧率和统计控制。沿用 `internal/privacy` 锁屏状态并提供可取消的状态变化订阅。锁屏状态和成败由当前会话发回，未装组件及 macOS 保留原因提示。支持新的锁屏状态提示时，控制端即使尚无视频也能看到控制中心。切换按采集世代忽略旧回调。密码只在需要时复制，所有完成和取消路径清理缓冲。第 4 次请求回报尝试过多并更新被控端提示。
- [ ] 重跑目标测试和全部检查，提交 `feat(app): switch desktop sessions on screen lock`。

## 任务 3　控制端凭据和控制中心

- [ ] 在 `internal/app/viewer_unlock_test.go` 用假凭据库和已验证的不同 HostKey 测试地址变化、公钥隔离、无可信公钥时禁用保存、记住后显式一键提交、输错后重新输入并覆盖、忘记、关闭会话和仅查看。对 `internal/ui/session_menu_test.go` 增加可见性及禁用原因测试，并验证弹窗复选框初始未勾选、Linux 不显示。为新增 `internal/unlockcred` 写平台集成测试，覆盖独立 service 或目标前缀下的保存、读取、删除。
- [ ] 运行 `go test ./internal/app ./internal/ui ./internal/unlockcred -run 'Test(Unlock|SessionMenu|Credential)'` 并检查失败。新增 `internal/unlockcred` 的 `Store` 接口及平台实现，提供 `Read(account string) ([]byte, error)`、`Write(account string, secret []byte) error`、`Delete(account string) error`。macOS 用 Security framework 通用密码，Windows 用原生凭据管理器，Linux 告知存储不可用。根据 `Peer.HostKey()` 生成账户指纹。
- [ ] 在 `internal/app/viewer.go` 和新增 `viewer_unlock.go` 接入协议、凭据及错误提示，在 `internal/ui/session_window.go` 增加「解锁电脑…」「忘记解锁密码」及掩码输入弹窗。仅在锁屏的桌面控制会话显示解锁，锁屏但不支持时禁用并显示原因。用已保存密码失败时显示「已保存的解锁密码不正确」，提供「重新输入」及「忘记解锁密码」。重新输入后以系统确认成功的值覆盖保存项。
- [ ] 重跑目标测试和全部检查，提交 `feat(viewer): unlock a host with a locally stored credential`。

## 任务 4　Windows 特权组件及本机通信

- [ ] 在 `internal/lockscreen` 写平台无关的 IPC 和生命周期测试，使用假系统身份与桌面。断言不同 SID、会话或可执行文件被拒绝，断开客户端、会话切换或解锁时立即停止，帧读取过程中发生缓冲翻转时不会返回混合帧。测试消息大小、帧尺寸与文件映射范围校验，确认密码清理以及日志仅含连接身份、操作类型和结果。
- [ ] 运行 `go test ./internal/lockscreen -run 'Test(IPC|Frames|Helper)'` 并检查失败。在 Windows 文件实现主程序的服务及助手模式。服务在正确的控制台会话以 LocalSystem 启动助手，管理退出和会话切换。助手在绑定的原生线程上验证当前输入桌面，复用 DXGI 和输入实现。命名管道限制本地客户端并验证当前交互用户 SID、控制台会话和受保护 exe 路径，逐次记录校验结果。主程序验证服务端后发送输入或凭据。画面用共享内存双缓冲与序号，受限范围只读映射给主程序。关闭连接时回收映射与句柄。
- [ ] 加入解锁和手动操作路径。用 Windows 支持的方式打开登录输入界面，查证 `SendSAS` 的系统策略要求，不修改机器策略。提交前再次确认本机仍锁定且是预期桌面，系统确认解锁后停用助手输入与采集。用假桌面覆盖竞争窗口，Windows 真机验证密码、PIN 和要求 Ctrl+Alt+Del 的登录界面。
- [ ] 重跑目标测试和全部检查，提交 `feat(windows): serve locked desktop sessions through local IPC`。

## 任务 5　安装、升级与卸载

- [ ] 用假 SCM 和文件操作覆盖重复安装、重复卸载、版本不符、拒绝 UAC、服务启动失败回滚、升级失败后恢复上一可运行版本、卸载可重试，以及目录或服务命令异常时拒绝修改。加一条测试验证主进程从可进行 IPC 身份验证的受保护副本运行，功能由用户主动安装后启用。
- [ ] 运行 `go test ./internal/lockscreen ./internal/ui -run 'Test(Install|Uninstall|Update|LockScreenSettings)'` 并检查失败。在 `main.go` 的普通 UI 和诊断初始化前分派模式。在 `internal/lockscreen` 的 Windows 实现中管理 SCM 和 Program Files 下的受保护副本，预检目录权限，串行化安装、升级与卸载。升级失败恢复旧文件和服务状态。设置安全页接入状态和 UAC 安装、升级、卸载入口。更新后比较主程序和组件版本，提示用户授权更新。
- [ ] 重跑目标测试和全部检查，提交 `feat(windows): manage the lock screen component`。

## 任务 6　Windows 测试包和验收文档

- [ ] 增加脚本验收，覆盖构建失败时退出、中文说明随包交付、最终 exe 哈希以及脚本生成的说明包括锁屏远控测试。以临时输出目录运行验收并观察预期失败。
- [ ] 更新 `scripts/build-windows.sh` 及安全、控制、FAQ 指南和发版说明。中文实测说明与 exe 同目录，包含设置安全页安装、Mac 控制 Windows、Win+L、看到画面、手动输入和控制中心解锁、密码或 PIN、记住与错误密码、初次不保存、仅查看、会话不断、Ctrl+Alt+Del、版本更新及卸载和清理。测试初始已锁时复连需按既有连接批准策略进行。
- [ ] 以工作区内的 `OUT_ROOT` 运行脚本验收和完整检查，核对文件、PE 架构、DLL 检查与哈希，再提交 `docs(windows): package and document lock screen testing`。用最终提交重新打包并报告测试包和说明路径。Windows 实机结果由用户执行验收后确认。

## 任务 7　macOS 被控端

- [ ] 读阶段 0 实测结果。有结果时按确认的服务形态实现。暂时无结果时采用随 app 打包的 root LaunchDaemon 和覆盖 Aqua、LoginWindow 的 LaunchAgent 助手。
- [ ] 在平台无关测试中覆盖锁定状态、活动会话、控制台 UID、Team ID、权限申请、授权撤销和安装清理。断言身份不符、状态未知或未锁定时拒绝转发。运行 `go test ./internal/lockscreen -run 'TestMacLockScreen'` 并检查失败。
- [ ] 接入 macOS 本地 IPC、采集、输入和解锁确认。`CGSessionCopyCurrentDictionary` 的锁定状态驱动既有切换路径。安装由用户在系统界面授权，界面报告所需权限。与 Windows 共用控制端和协议，保留平台各自的运行条件。
- [ ] 跑完整检查和普通权限可运行的构建步骤，编写中文真机说明。第一项验证锁屏后能否看到画面和输入，再验证密码解锁、切换恢复、安装和卸载。提交 `feat(macos): support remote sessions at the lock screen`。
