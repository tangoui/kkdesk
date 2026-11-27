# 远程光标形状同步设计

日期：2026-11-27
状态：用户已同意

## 为什么

远程控制时，被控端不把鼠标画进视频（`4b6aa24` 为画面清晰去掉了），控制端显示的是自己的本地光标，永远是箭头。指到对方的链接、按钮、输入框上时看不到小手或文本竖线，不知道哪里能点。UU 远程会同步对方的光标形状，用户希望一样。

## 目标与非目标

- 目标：被控端（macOS、Windows）光标形状变化时通知控制端；控制端在远程画面区域把本地光标换成对应形状。远程控制和仅查看会话都同步；仅传文件会话不涉及。
- 非目标：传输光标图片、自定义光标图案、光标位置同步（本地光标本来就跟着用户鼠标）。不能识别的形状一律显示默认箭头。

## 方案

### 形状集合（以 Tango `ui/input.Cursor` 为准）

箭头（Default）、小手（Pointer）、文本竖线（Text）、禁止（NotAllowed）、左右调整（ResizeEW）、上下调整（ResizeNS）、移动（Move）、抓取（Grab）、抓取中（Grabbing）。被控端识别出其他形状（等待、十字、斜向调整等）时发"箭头"。线上编码与 Tango 枚举解耦（kkdesk 自己定义一组字节值），控制端再映射到 Tango 枚举。

### 被控端检测

- Windows：`GetCursorInfo` 取当前 `HCURSOR`，与 `LoadCursor(NULL, IDC_ARROW / IDC_HAND / IDC_IBEAM / IDC_NO / IDC_SIZEWE / IDC_SIZENS / IDC_SIZEALL / ...)` 的系统句柄比较。光标隐藏（`CURSOR_SHOWING` 未置位）时按箭头处理。
- macOS：`NSCursor.currentSystemCursor`，与 `arrowCursor / pointingHandCursor / IBeamCursor / operationNotAllowedCursor / resizeLeftRightCursor / resizeUpDownCursor / openHandCursor / closedHandCursor` 比较（对象比较不可靠时比较热点与图像数据的哈希，实现者实测选择）。需要的权限不超过被控端现有权限。
- 采样：在被控端已有的采集循环或独立的低频轮询（如 30–60 Hz，实现者选择）里检测，**只在形状变化时**发消息；会话开始时发一次当前形状。
- 检测失败或平台不支持时不发消息，控制端保持箭头。

### 协议

meta 通道新增"光标形状"消息（被控端 → 控制端，1 字节形状码），认证通过并放行后才发，控制端未认证时忽略（沿用现有闸门）。字节值由实现者定，避开已用值。不需要兼容旧版本。

### 控制端显示

- 鼠标在远程画面（`internal/ui/session_window.go` 的 NativeView）上时显示对方形状；离开画面区域恢复正常界面光标。
- **Tango 依赖**：当前 Tango 的 `ui.NativeView` 固定以 `input.CursorDefault` 交互，不能改光标。需要在 Tango 里给 `NativeViewW` 加一个设置光标的方法（如 `Cursor(input.Cursor) *NativeViewW`，命名按 Tango 风格），三平台后端按现有光标机制生效。这是 Tango 仓库的独立改动，遵守 Tango 的规则（见下）。

## Tango 改动的规则

- 仓库 `/Users/admin/min/tango`，在独立 worktree 里改，不动主工作区未提交的改动。
- 提交作者固定 `tango <903300326@qq.com>`（仓库已配置），**提交信息不加 Co-Authored-By**。
- 推送前检查：`gofmt -l .`（含构建标签文件）为空、`go test ./...`、`GOOS=linux go vet ./...`、`GOOS=windows go vet ./...`、`apitxt -check`（公开 API 变化要更新 api/go/*.txt）。
- 不合并、不推送；由派单会话审核后推送 Tango，再把 kkdesk 的 Tango 依赖升级到推送后的版本。开发期间 kkdesk 可以用临时 `go.work` 或 `replace` 指向本地 Tango worktree，但**不得把 replace 提交进 kkdesk**。

## 测试

- 被控端：形状映射表（各平台系统光标 → 线上码）单元测试；只在变化时发送；会话开始发一次；未知形状发箭头。
- 协议：认证前不处理、files-only 不发、往返测试。
- 控制端：线上码 → Tango 枚举映射；远程画面区域的光标随消息变化（用 Tango 测试驱动检查节点的光标）。
- Tango：NativeView 光标方法的测试。
- 全部检查：kkdesk 的 `gofmt`、`go vet`、`go test`、相关包 `-race`、Linux/Windows vet；Tango 按上面的推送前检查。

## 我替用户做的决定

1. 只同步形状、不传图片；Tango 不支持的形状显示箭头。
2. 仅查看会话也同步。
3. 需要给 Tango 加 NativeView 光标接口，Tango 与 kkdesk 分两个分支，Tango 先审先推。
