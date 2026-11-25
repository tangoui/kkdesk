# 构建与运行

## 准备

- Go 1.27
- [`tan`](https://github.com/tangoui/tango/tree/main/cmd/tan)：Tango 的命令行工具
- macOS：Xcode 命令行工具（cgo 需要）
- 交叉编译 Windows 版：mingw-w64（`brew install mingw-w64`）

项目用 `tan init` 的标准布局：入口 `main.go` 在仓库根目录，`tan.json` 里打开了 `"cgo": true`。

## 开发运行

```sh
tan dev
```

打开主窗口并开启热重载。改 Go 代码后窗口会自动重启。

::: tip 权限
`tan dev` 下，macOS 的「录屏与系统录音」「辅助功能」权限记在启动它的终端应用上，请给终端开权限。
:::

想在同一台 Mac 上再开一个独立实例（例如测试控制端），用另一个配置目录：

```sh
KKDESK_CONFIG_DIR=/tmp/kkdesk-b go run . -listen :21119
```

## 构建 macOS 版

```sh
CGO_ENABLED=1 go build -o kkdesk .
```

## 交叉编译 Windows 版

在 Mac 上：

```sh
scripts/build-windows.sh
```

产物放在 `outputs/kkdesk-spike/win/<日期>-<序号>/`（可用 `OUT_ROOT` 改位置），包括：

- `kkdesk.exe`：windows/amd64，cgo + mingw-w64 静态链接，只依赖系统 DLL（脚本会用 objdump 检查）；
- `README.txt`：给测试者的中文说明；
- `build.log`：构建日志。

`kkdesk.exe` 是控制台程序：运行时会同时打开一个 PowerShell 窗口显示诊断信息，主窗口照常打开。

## 构建跨网服务

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o kkdesk-server ./cmd/kkdesk-server
```

部署见 [部署跨网服务](/deploy/server)。

## 构建本文档

文档站用 VitePress，源文件在 `docs/`：

```sh
cd docs
pnpm install
pnpm dev       # 本地预览
pnpm build     # 输出到 docs/.vitepress/dist
```

`docs/superpowers/`、`docs/design/` 和 `docs/spike-report.md` 是内部的设计记录，不进文档站。

文档站发布在 <https://tangoui.github.io/kkdesk/>。main 分支上 `docs/` 有改动时，GitHub Actions（`.github/workflows/docs-pages.yml`）会自动构建并发布。
