# kkdesk 手机端第 1 期模拟器截图

[第二轮真机反馈版截图](round-2/README.md)包含三栏导航、传输页和更新后的弹窗。

| 页面 | iOS | Android |
| --- | --- | --- |
| 设备 · 浅色 | [iOS](ios/01-devices-light.png) | [Android](android/01-devices-light.png) |
| 扫码 | [iOS](ios/02-scan.png) | [Android](android/02-scan.png) |
| 核对 · 等待电脑确认 | [iOS](ios/03-pair-waiting.png) | [Android](android/03-pair-waiting.png) |
| 核对 · 成功 | [iOS](ios/04-pair-success.png) | [Android](android/04-pair-success.png) |
| 首页已配对提示 | [iOS](ios/05-paired-banner.png) | [Android](android/05-paired-banner.png) |
| 核对 · 拒绝 | [iOS](ios/06-pair-rejected.png) | [Android](android/06-pair-rejected.png) |
| 核对 · 超时 | [iOS](ios/07-pair-timeout.png) | [Android](android/07-pair-timeout.png) |
| 核对 · 二维码过期 | [iOS](ios/08-pair-expired.png) | [Android](android/08-pair-expired.png) |
| 远程 · 触控板 | [iOS](ios/09-remote-trackpad.png) | [Android](android/09-remote-trackpad.png) |
| 远程 · 悬浮球菜单 | [iOS](ios/10-remote-menu.png) | [Android](android/10-remote-menu.png) |
| 远程 · 直接点按 | [iOS](ios/11-remote-direct.png) | [Android](android/11-remote-direct.png) |
| 远程 · 键盘和快捷键 | [iOS](ios/12-remote-keyboard.png) | [Android](android/12-remote-keyboard.png) |
| 我的 | [iOS](ios/13-mine.png) | [Android](android/13-mine.png) |
| 设备 · 深色 | [iOS](ios/14-devices-dark.png) | [Android](android/14-devices-dark.png) |

在 `mobile/` 目录运行截图脚本。将 `KKDESK_SHOT_PLATFORM` 设为 `ios` 或 `android`，并将 `KKDESK_SHOT_DEVICE` 设为对应的模拟器 ID。Android 需设置 `ANDROID_HOME`。脚本通过模拟器采集整屏，包含系统状态栏和系统软键盘。扫码取景和桌面视频使用设计稿中的演示内容。Android 模拟器首次进入沉浸模式时，确认系统的全屏提示后再采集。

```sh
flutter drive -d "$KKDESK_SHOT_DEVICE" --debug \
  --target integration_test/demo_screens.dart \
  --driver test_driver/demo_screens_test.dart
```
