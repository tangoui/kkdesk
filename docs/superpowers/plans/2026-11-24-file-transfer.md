# kkdesk 文件传输 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 控制端和被控端之间双向传文件和文件夹：浏览对方的目录，上传、下载、取消，显示进度；被控端可以随时关闭文件访问权限。

**Architecture:** 在现有 WebRTC 连接上新增一条有序可靠的数据通道 `files`。控制端是客户端，被控端是文件服务器。控制消息用 JSON（带请求 id），文件内容用二进制分块（每块带传输 id 和偏移）。发送方用数据通道的 `BufferedAmount` / `OnBufferedAmountLow` 做流量控制。引擎在 `internal/files`（纯 Go，可在任何平台单测），界面是 Tango 自绘的「文件传输」窗口（`internal/ui`）。

**Tech Stack:** Go、pion WebRTC 数据通道、Tango ui。

**Spec / 设计来源:** 界面原型 `docs/design/prototype.html` 的「文件传输」页（左本机、右对方、下方队列）；设计规格 `docs/superpowers/specs/2026-11-24-kkdesk-spike-design.md` 的产品形态。用户 2026-11-24 要求「文件传输功能做好」，下列决定由实现方确定并已告知用户。

## Global Constraints

- 通道：`files`，`Ordered: true`，可靠（默认重传）；由控制端在 offer 前创建（同 `input`），被控端在 OnDataChannel 收到。连接模式「仅传文件」时不启动采集和编码，只建数据通道。
- 协议（版本号 1，握手时交换）：
  - 请求（控制端→被控端，JSON 文本消息）：`{"id":n,"op":"list","path":p}`、`stat`、`home`（返回被控端用户主目录和盘符/卷列表）、`mkdir`、`download`（`path`：文件或文件夹；被控端随后推送内容）、`upload_begin`（`dir`、相对路径、`size`、`mode`、`mtime`）、`cancel`（`transfer`）。
  - 响应：`{"id":n,"ok":true,...}` 或 `{"id":n,"ok":false,"error":"..."}`；错误码 `denied`（权限关闭）、`not_found`、`exists`、`io`。
  - 数据：二进制消息 `[1 字节类型][8 字节传输 id][8 字节偏移][数据]`，块大小 64 KiB；结束消息 JSON `{"op":"end","transfer":t,"sha256":hex}`，接收方校验大小和 SHA-256，不一致则报错并删除临时文件。
- 写文件：先写到同目录 `.<名字>.kkdesk-part`，完成并校验后原子重命名；重名时改为 `名字 (1).扩展名`、`(2)`……；取消或出错时删除临时文件。保留修改时间（尽力而为）。
- 文件夹：递归；先建目录结构，再逐个文件传；符号链接不跟随（作为链接本身跳过并记录）；单个文件失败不影响其他文件，最后汇总。
- 流量控制：发送方在 `BufferedAmount > 4 MiB` 时暂停，`OnBufferedAmountLow`（阈值 1 MiB）后继续；同一时间只发一个文件（队列串行），列目录请求可以穿插。
- 权限：被控端 `FileServer.SetAllowed(bool)`；关闭时所有请求返回 `denied`，进行中的传输中止。默认允许（与连接确认弹窗里「文件传输」勾选一致）。
- 路径：被控端接受绝对路径；Windows 用 `C:\...` 形式，`home` 返回可用卷；路径在被控端用 `filepath` 规范化。列目录返回 `name、is_dir、size、mtime、hidden`，目录在前、按名称排序；单次最多 5000 项并标注截断。
- 界面：「文件传输」窗口：顶部工具条（对方名称、上传 →、← 下载、新建文件夹、刷新），左右两个面板（路径栏可编辑、上级目录按钮、列表多选、双击进入），底部传输队列（文件名、方向、进度条、速度、剩余时间、取消；完成和失败状态）。入口：会话「控制中心」菜单「工具 → 文件传输」，主窗口连接方式「传文件」。
- 日志与统计：每个传输记录字节数、耗时、平均速度到主机/控制端日志。
- 不复制第三方代码。提交规则同前。

## Review Focus

1. 传输中断开连接或关闭窗口：两端都删掉临时文件，不留半截文件，引擎 goroutine 退出。
2. 大文件（≥ 2 GB）：内存占用不随文件大小增长（流量控制有效），进度和速度正确。
3. 同名文件、目标是只读目录、磁盘满：给出明确错误，不覆盖已有文件。
4. 被控端在传输中关闭「文件传输」权限：传输立即中止，控制端显示「对方已关闭文件访问」。
5. Windows 与 macOS 之间：路径分隔符、盘符、文件名大小写、非法字符（Windows 不允许 `:*?"<>|`）——下载到 Windows 时非法字符替换为 `_`。

---

### Task F1: 传输引擎与会话接入（纯 Go）

**Files:** `internal/files/{proto.go,server.go,client.go,transfer.go,names.go}` + 测试；`internal/session/peer.go`（新增 `files` 通道，`Peer.FileChannel()` 或直接 `Peer.Files() *files.Client / *files.Server`）；`internal/session` 的连接选项增加「仅传文件」模式。

**Interfaces:**
- `files.NewServer(ch Channel, allowed func() bool) *Server`；`files.NewClient(ch Channel) *Client`；`Channel` 是对数据通道的抽象（发文本、发二进制、OnMessage、BufferedAmount、OnBufferedAmountLow），便于用内存管道单测。
- `Client.Home(ctx) (Home, error)`、`List(ctx, path) ([]Entry, error)`、`Mkdir(ctx, path) error`、`Download(ctx, remotePath, localDir string, progress func(Progress)) (*Transfer, error)`、`Upload(ctx, localPath, remoteDir string, progress func(Progress)) (*Transfer, error)`；`Transfer.Cancel()`、`Wait() (Result, error)`；`Progress{Name string; Done, Total int64; Files, FilesDone int; BytesPerSec float64}`。
- 测试：内存管道上：列目录（排序、截断、隐藏文件标记）；下载与上传单文件（含 0 字节、5 MB、跨块边界）和目录树；重名改名；校验失败删除临时文件；取消；权限关闭中止；Windows 非法字符替换（纯函数）；然后在 `internal/session` 用真实 Peer（127.0.0.1）传 50 MB 文件，记录吞吐。

- [ ] 测试先行 → 实现 → `go test -race ./internal/files/ ./internal/session/` → 提交。

### Task F2: 文件传输窗口与接线

**Files:** `internal/ui/files_window.go`（及拆分）、`internal/app/viewer.go`（打开文件窗口、仅传文件模式）、`internal/app/host.go`（FileServer 权限与连接确认、右下角面板「文件传输」开关生效）、`main.go` / 主窗口「传文件」模式。

- [ ] 实现界面与接线；Mac 自连（`-connect 127.0.0.1` 仅传文件模式）后台实测：浏览、上传一个文件夹、下载一个大文件、取消、关闭权限；截图对照原型。
- [ ] Windows 交叉编译仍通过；提交。
