# 测试与测量

## 单元测试

```sh
go test ./...
go test -race ./internal/session ./internal/app ./internal/clipsync
```

`internal/session` 和 `internal/app` 的测试会在本机 loopback 上建立真实的 WebRTC 连接。

## 推送前检查

```sh
gofmt -l .
go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
  CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go vet ./...
```

改了跨网服务时，再检查 Linux 版：

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./cmd/... ./internal/rendezvous/...
```

## 桌面端到端测试

端到端测试会启动真实的 kkdesk 窗口，通过 Tango 的自动化通道操作界面。窗口在后台打开，不抢前台。只有设置了 `TANGO_E2E=1` 才会运行。

| 测试 | 命令 | 覆盖 |
|---|---|---|
| 导航页面 | `TANGO_E2E=1 KKDESK_E2E_NAV=/tmp/kkdesk-nav go test -run E2ENav -v .` | 我的设备、远程会话、文件传输、设置的主要操作；监听端口 21918 → 21919。 |
| 文件传输 | `TANGO_E2E=1 KKDESK_E2E_FILES=/tmp/kkdesk-files go test -run E2EFiles -v .` | 文件窗口的浏览、上传下载、取消。 |
| 跨网连接 | `TANGO_E2E=1 go test -run E2ERemote -v .` | 本机起信令和 TURN 服务（强制走中继），两个实例用设备码连接。 |

加 `KKDESK_E2E_SHOTS=<目录>` 会把界面截图存到该目录（本文档里的截图就是这样来的）。每个测试用自己的配置目录，不会动你平时用的配置。

## 自连测量

在一台 Mac 上测端到端延迟和帧率：

```sh
scripts/loopback.sh <输出目录> [秒数] [被控端参数...]
```

脚本会打开一个持续变化的动画窗口，以 `-host` 共享屏幕，再用 `-connect 127.0.0.1` 仅查看。两边的统计分别写进 `host.jsonl` 和 `view.jsonl`，结束时自动汇总到 `summary.txt`。也可以手动汇总任意日志：

```sh
go run ./scripts/summary host.jsonl view.jsonl
```

已有结果（Mac 自连）：

| 指标 | 结果 |
|---|---|
| 端到端延迟 | 中位数 33 ms，P95 50 ms |
| 帧率 | 约 30 fps |

自连时系统要同时合成被采集的画面和显示远端画面，屏幕采集会被降频，所以帧率只有约 30 fps。真实帧率要在两台电脑之间测。

## Windows 自检

在 Windows 上运行 `.\kkdesk.exe -selftest`，它会依次检查 DXGI 采集、Media Foundation 编码和 SendInput 注入，结果写进 `kkdesk-selftest.log`，编码出的码流存为 `selftest.h264`，可以用播放器检查。
