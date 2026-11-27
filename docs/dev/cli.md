# 命令行参数

不带参数运行时打开主窗口。下面的参数主要用于测量、自检和调试，**优先于配置文件**。

```text
kkdesk -selftest
kkdesk -host [-autoaccept] [-fps N] [-bitrate Mbps] [-log file]
kkdesk -connect addr -password pw [-view-only | -files] [-log file]
```

## 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-host` | | 被控端模式：共享本机屏幕并接受连接，启动时打印监听地址和临时密码。 |
| `-connect addr` | | 控制端模式：连接 `addr` 上的被控端，直接打开会话窗口。 |
| `-password pw` | | 控制端：对方的临时密码。被控端：本次运行使用这个临时密码，不自动更换。 |
| `-view-only` | `false` | 控制端：只看不操作。 |
| `-files` | `false` | 控制端：只传文件，打开文件窗口，不显示远端画面。 |
| `-listen addr` | 设置里的端口（21118） | 被控端监听地址，例如 `:21918`。用它时，端口被系统保留后换到的新端口不会保存。 |
| `-fps N` | `60` | 帧率：30、60、90 或 144。 |
| `-bitrate Mbps` | `0` | 码率，0 表示自动（8 Mbps）。 |
| `-maxwidth N` | `1920` | 被控端：采集画面缩小到不超过这个宽度，0 表示原始分辨率。 |
| `-autoaccept` | `false` | 被控端：不询问，直接接受连接（测量用）。 |
| `-log file` | | 把每秒的统计数据写成 JSON Lines。 |
| `-applog file` | Windows 和 macOS 的配置目录下的 `kkdesk.log` | 人类可读的诊断日志，`""` 表示不写。 |
| `-selftest` | | 不开界面、不联网，检查屏幕采集、编码和输入注入，写出 `kkdesk-selftest.log` 和 `selftest.h264` 后退出，退出码 0 表示通过。 |

## 说明

- 诊断日志所在的配置目录默认为 Windows 的 `%APPDATA%\kkdesk` 或 macOS 的 `~/Library/Application Support/kkdesk`。
- Windows 上 `kkdesk-selftest.log` 和 `selftest.h264` 写在 `kkdesk.exe` 所在目录，目录不可写时写到当前目录。macOS 上写到当前目录。

- 给了 `-host` 或 `-password` 时，临时密码在本次运行中不更换，方便测量脚本重复连接。
- `-host` 模式不同步剪贴板，避免测量时动到系统剪贴板。
- 环境变量 `KKDESK_CONFIG_DIR` 指定配置目录，见 [设置 › 配置文件](/guide/settings#配置文件)。
