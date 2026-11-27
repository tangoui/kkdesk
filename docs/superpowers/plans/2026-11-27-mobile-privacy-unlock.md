# Mobile Privacy, Lock, Unlock, and Cursor Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans and implement each task test-first with a separate commit.

**Goal** 补齐手机控制端的隐私屏、断开锁屏、远程解锁、锁屏提示和触控板光标。

**Architecture** 两种信令沿用同一个 WebRTC meta 数据通道。Dart 协议层解析 Go 帧并保留会话状态，解锁凭据流程独立于远控界面，界面使用现有菜单与触控模式。

**Tech Stack** Dart、Flutter、flutter_webrtc、local_auth、flutter_secure_storage。

**Spec** [手机控制端设计](../specs/2026-11-27-mobile-privacy-unlock-design.md)

## Global Constraints

- 只改 `mobile/` 和本计划。Go 实现决定线协议，桌面端决定提示文案和菜单行为。
- 覆盖手输密码局域网、配对直连与配对跨网，认证前丢弃功能帧且禁止发送。
- 文件会话隐藏功能入口，仅查看隐藏解锁，只有触控板模式绘制光标。
- 使用现有依赖和公钥标识存储。解锁密码不进日志。系统验证失败或取消时禁止发送已保存密码。
- 每项任务先运行新增用例确认预期失败，实现后运行完整检查才提交。每次提交正文附 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。

## Checks

```sh
cd mobile
/Users/admin/development/flutter/bin/flutter pub get --offline && /Users/admin/development/flutter/bin/dart analyze lib test && /Users/admin/development/flutter/bin/flutter test
```

每项任务和最终检查都应通过。真机检查待交付后执行，无需启动模拟器。

## Review Focus

- 手输密码认证成功帧和身份写入之间到达的第一条锁屏状态仍须可见。
- 配对直连失败后转跨网时，旧 peer 的消息和公钥不能改变当前会话。
- 正常退出应在关闭通道前发送 `[0x62]`，主机才能立即执行断开锁屏。
- 生物识别取消、页面退出或锁屏状态变化时，不发送保存的密码。
- 初次连接时对方已锁屏且没有视频帧，远控界面仍显示锁屏原因。

### Task 1. Meta protocol

**Files** `mobile/lib/desktop_meta.dart`、`mobile/test/desktop_meta_test.dart`。

- [ ] 先写协议用例。断言开关请求为 `[0x90, 1]`、`[0x92, 0]` 和 `[0x94]`。基于 Go 固定值校对 `0x91/0x93/0x95` 状态、`[0x98, 2..10]` 锁屏原因、`0x99` 解锁请求、`0x9a` 结果和 `0x9b` 光标。拒绝错误方向、长度、状态、UTF-8 和包含 NUL 的解锁密码，未知光标回退箭头。
- [ ] 运行 `flutter test test/desktop_meta_test.dart`，确认失败来自协议实现缺失。
- [ ] 实现 `DesktopMeta.decode(List<int>)`、三个隐私请求编码函数及 `encodeUnlockRequest(Uint8List)`。分别返回类型化的状态、结果与光标。
- [ ] 运行完整检查并提交 `feat(mobile): implement desktop meta protocol`。

### Task 2. Authenticated transport and session state

**Files** `mobile/lib/lan_session.dart`、`mobile/lib/lan_remote_session.dart`、`mobile/lib/mobile_services.dart`、`mobile/test/desktop_session_test.dart`。

- [ ] 在 Flutter WebRTC 平台边界写测试。手输密码会话挂起身份写入，先注入认证前的锁屏帧，再在成功结果后注入合法初始帧。检查前者被丢弃，后者在认证完成后可见。配对直连及跨网检查答复签名和 peer 就绪前后的状态与发送，文件会话全程屏蔽。确认普通断开先发 `[0x62]`，锁屏并断开失败时保持连接，成功时由主机发送断开帧。
- [ ] 运行 `flutter test test/desktop_session_test.dart`，观察上述功能断言失败。
- [ ] 在 `LanSession` 接入 meta 收发，提供已验证的 `hostKey`、状态更新回调和显式的正常断开。配对取 `pair.transcript.hostKey`，手输密码取认证后记住的主机公钥。状态只应用于当前桌面 peer，重试中丢弃旧 peer 的状态。通过 `LanRemoteSession` 暴露给远控界面。
- [ ] 运行完整检查并提交 `feat(mobile): wire authenticated desktop controls`。

### Task 3. Unlock and credentials

**Files** `mobile/lib/remote_unlock.dart`、`mobile/lib/lan_remote_session.dart`、`mobile/test/remote_unlock_test.dart`。

- [ ] 用 `FlutterSecureStorage.setMockInitialValues` 与本机身份验证替身覆盖未存密码、验证通过才读取及提交、失败和取消、设备密码回退、无验证能力、忘记、不同公钥隔离、密码错误和尝试上限。断言只有主机报告成功才记住手输密码，退出或过期回包清除会话内凭据。
- [ ] 运行 `flutter test test/remote_unlock_test.dart`，观察解锁流程用例失败。
- [ ] 实现 `RemoteUnlock`，使用现有 `LocalAuthentication` 和 `FlutterSecureStorage`。单个会话只允许一个未完成请求，发送和等待结果时保留必要字节，完成、失败、超时和关闭时清理。结果使用桌面端文案。
- [ ] 运行完整检查并提交 `feat(mobile): protect saved remote unlock credentials`。

### Task 4. Remote menu, notices, and cursor

**Files** `mobile/lib/ui/remote.dart`、`mobile/test/remote_desktop_controls_test.dart`。

- [ ] 写组件测试。验证等待同意及主机回报驱动开关，未知和已解锁不显示解锁，诊断状态显示原因并禁用解锁，仅查看和仅传文件限制入口。检查输入框默认不记住密码，错误时显示重新输入和忘记，触控板光标随主机变化且直接点按时隐藏。
- [ ] 运行 `flutter test test/remote_desktop_controls_test.dart`，观察新增菜单与光标行为用例失败。
- [ ] 在远控菜单复用现有行组件，并在会话视图显示当前锁屏原因。使用原生安全输入框和既有 Flutter 元素呈现解锁与光标。
- [ ] 运行完整检查并提交 `feat(mobile): show privacy and unlock controls in remote view`。

## Device Verification

- iOS。手输密码局域网连接，验证隐私屏等待与同意、退出后锁屏，再以手输密码解锁并保存。锁屏重连后用 Face ID 解锁，取消一次确认电脑保持锁屏，最后忘记密码。
- Android。分别经配对直连和跨网连接，核对锁屏原因、锁屏并断开及触控板光标。使用未录制指纹但设置设备 PIN 的手机验证已保存密码的系统验证流程，切到直接点按检查光标隐藏。
