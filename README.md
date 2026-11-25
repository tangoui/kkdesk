# kkdesk

用 [Tango](https://github.com/tangoui/tango) 写的远程桌面：Go 程序，自绘界面，远端画面用系统原生视频视图显示。

一个程序两种角色：被控端（共享本机屏幕）和控制端（查看、控制对方），点对点直连，不经过服务器。

## 现状（验证原型）

- macOS：被控端和控制端都可用。屏幕采集用 ScreenCaptureKit，编解码用 VideoToolbox H.264 硬件编解码，画面用 `AVSampleBufferDisplayLayer` 显示。
- Windows：被控端已写完，尚未实机验证。采集用 DXGI Desktop Duplication，编码用 Media Foundation H.264（优先硬件），注入用 SendInput。
- 传输：pion WebRTC。视频是 H.264 RTP；输入、画质切换、时钟同步、文件传输走数据通道。连接时用 6 位临时密码（或设置里的固定密码）做挑战应答。
- 文件传输：浏览、上传下载文件和文件夹、校验、取消、权限开关；「文件传输」页列出会话和本次运行的传输记录。
- 我的设备、远程会话、设置：通讯录（分组、在线探测）、会话列表、固定密码与临时密码更换策略、端口、主题，数据只存本机。
- Mac 自连测量见 [docs/spike-report.md](docs/spike-report.md)：延迟中位数 33 ms、P95 50 ms；帧率受系统屏幕采集降频影响，约 30 fps。

设计与计划：
- [docs/superpowers/specs/2026-11-24-kkdesk-spike-design.md](docs/superpowers/specs/2026-11-24-kkdesk-spike-design.md)
- [docs/superpowers/plans/](docs/superpowers/plans/)
- 界面原型：[docs/design/prototype.html](docs/design/prototype.html)

## 构建与运行

需要 Go 1.27 和 [`tan`](https://github.com/tangoui/tango/tree/main/cmd/tan)。项目用到 cgo（`tan.json` 里 `"cgo": true`）。

```sh
tan dev                                   # 打开主窗口，热重载
CGO_ENABLED=1 go build -o kkdesk .         # macOS 构建
./kkdesk -host                            # 被控端：打印本机地址和临时密码
./kkdesk -connect 192.168.1.20:21118 -password 123456   # 控制端
./kkdesk -selftest                        # 采集、编码、注入自检
```

Windows 版在 macOS 上交叉编译（需要 mingw-w64）：

```sh
scripts/build-windows.sh                  # 产出 kkdesk.exe 和说明
```

测量：`scripts/loopback.sh <输出目录> [秒数]`（本机自连），汇总用 `go run ./scripts/summary <日志...>`。

## 配置

「设置」和「我的设备」保存在本机的 `kkdesk.json`：macOS `~/Library/Application Support/kkdesk/`，Windows `%APPDATA%\kkdesk\`。环境变量 `KKDESK_CONFIG_DIR` 指定另一个目录（例如在 `tan dev` 旁边再开一个测试实例）。命令行参数（`-listen`、`-password`、`-fps`、`-bitrate`、`-autoaccept`）优先于配置文件；给了 `-password` 或 `-host` 时临时密码在本次运行中不更换。文件损坏时会备份为 `kkdesk.json.bad` 并用默认设置启动。

四个导航页面的端到端测试：`TANGO_E2E=1 KKDESK_E2E_NAV=/tmp/kkdesk-nav go test -run E2ENav -v .`（独立配置目录，端口 21918 → 21919）。

## 权限

- macOS：被控端需要「屏幕录制」权限，控制别人操作本机时需要「辅助功能」权限（系统设置 › 隐私与安全性）。
- Windows：首次监听端口时请在防火墙提示里允许（专用网络）。以普通权限运行时，无法控制管理员窗口和 UAC 弹窗。
