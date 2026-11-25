# kkdesk 手机端

Flutter iOS 和 Android 客户端。首页的演示设备支持查看和控制界面、配对流程、两种触控、画质与屏幕选择，以及剪贴板交互。右上角「添加电脑 → 局域网地址 + 密码」使用真实的 WebRTC 连接，默认端口为 21118。iOS 首次连接可能提示访问本地网络。

## 运行和验证

```sh
flutter pub get
flutter analyze
flutter test
flutter run -d <模拟器ID>
```

「输入配对码」接受 8 位数字。使用以下号码可复现各个核对结果。

| 配对码 | 演示结果 |
| --- | --- |
| `12345678` | 等待电脑确认，随后配对成功 |
| `22222222` | 配对被拒绝 |
| `33333333` | 配对超时 |
| `44444444` | 二维码已过期 |

正式入口使用系统相机和相册。截图入口使用原型的静态取景演示和模拟服务。截图目录包含两平台的界面和复现命令。[模拟器截图](../docs/mobile-ui-shots/README.md)

「传输」显示本机最近的连接记录，以及本次运行内的剪贴板同步记录。点文字片段可复制原文。文件传输入口标为第 2 期。

## 结构和依赖

- `DeviceRepository` 管理设备列表、改名和解除配对。`PairingService` 发起配对，返回电脑身份、服务器、核对码和状态流。`RemoteSessions` 创建会话，`RemoteSession` 提供视频组件、状态、指针和键盘输入，以及画质、屏幕和剪贴板操作。
- 页面使用 Flutter 原生 `StatefulWidget`、`ChangeNotifier` 和 `StreamBuilder`。演示配对设备、服务器地址、每台电脑的触控模式、App 锁和主题保存在 `shared_preferences`。局域网连接密码仅在当前会话内使用。
- `mobile_scanner` 扫描相机或图片中的二维码，`image_picker` 打开系统相册。`local_auth` 验证面容 ID 或指纹，用于 App 锁和免确认连接。`flutter_slidable` 提供设备行的左滑操作。
- `TouchController` 是纯 Dart 手势状态机。连接层接收归一化的鼠标事件。当前远程软键盘将英文、数字及常见符号映射为 USB HID，同时提供可锁定的修饰键和快捷键栏。
- `LanRemoteSession` 通过 `flutter_webrtc` 的 `RTCVideoView` 显示局域网视频。现有 `LanSession` 仍负责握手和传输。`lan_session_mobile_controls.dart` 复用已建立的数据通道发送键盘、滚轮、右键及拖拽事件，并读取时钟往返时间和视频帧率。第 0 步的性能采集参数仍可使用。[第 0 步报告](../docs/mobile-spike-report.md)

## 接入范围

跨网与配对协议、电脑确认、在线状态、设备凭据均由演示服务提供。接入真实配对后，设备密钥与凭据需要使用系统安全存储。当前真实局域网会话支持画面、鼠标和 USB HID 键盘，画质、屏幕切换及剪贴板保留演示入口。文件传输和隐私屏的操作置灰。
