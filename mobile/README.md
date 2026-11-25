# kkdesk 手机端

Flutter 局域网验证版。输入电脑端显示的局域网地址和连接密码。默认端口为 21118。电脑端接受连接后，手机显示远程画面，支持单指点击和拖动。

```sh
flutter pub get
flutter analyze
flutter test
flutter run -d <设备ID>
```

iOS 设备上的首次连接需要允许访问本地网络。测试构建和测量口径见 [第 0 步报告](../docs/mobile-spike-report.md)。
