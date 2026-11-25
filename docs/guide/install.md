# 安装与首次运行

正式版本发布在 [GitHub Releases](https://github.com/tangoui/kkdesk/releases)，装好后会[自动更新](./update)。第一个正式版本发布之前，请[从源码构建](/dev/build)。

| 系统 | 下载 | 安装 |
|---|---|---|
| macOS（Apple 芯片） | `kkdesk-<版本>-darwin-arm64.app.tar.gz` | 双击解压，把 `kkdesk.app` 拖进「应用程序」。 |
| macOS（Intel） | `kkdesk-<版本>-darwin-amd64.app.tar.gz` | 同上。 |
| Windows（x64） | `kkdesk-<版本>-windows-amd64.exe` | 不需要安装，放进一个固定的文件夹双击运行。只依赖系统自带的 DLL。 |
| Linux（x64、arm64） | `kkdesk-<版本>-linux-<架构>.tar.gz` | 解压到一个单独的文件夹运行。Linux 版目前**只能传文件**，不能共享屏幕或查看远端画面。 |

::: tip macOS 第一次打开
发布版用开发者证书签名，但还没有经过苹果公证。第一次打开时如果提示「无法验证开发者」，请在「应用程序」里**右键点 kkdesk › 打开**，再点「打开」。之后正常双击即可，自动更新也不会再提示。
:::

从源码构建时：

- **macOS**：在 Mac 上构建，开发时用 `tan dev` 直接运行。
- **Windows**：在 Mac 上用 `scripts/build-windows.sh` 交叉编译，得到一个 `kkdesk.exe`。

## macOS

### 屏幕录制权限（被控端必需）

别人要看到这台 Mac 的屏幕，kkdesk 必须有「屏幕录制」权限。开启「允许被远程控制」后，如果还没有这个权限，kkdesk 会弹出「需要屏幕录制权限」：

1. 点 **打开系统设置**，会直接跳到「隐私与安全性 › 录屏与系统录音」。
2. 打开 kkdesk 的开关。列表里没有时点「＋」添加。
3. 如果 macOS 提示退出并重新打开，照做即可。

关掉弹窗后，连接页「复制并分享」旁边会一直显示 **配置屏幕录制权限** 按钮，直到权限开好为止。

::: tip 用 tan dev 运行时
权限记在启动 `tan dev` 的**终端应用**上（例如 Terminal、iTerm、MinCode），而不是 kkdesk。请给这个终端应用开「录屏与系统录音」和「辅助功能」。
:::

### 辅助功能权限（被别人控制时）

要让对方的键盘鼠标操作生效，还需要「辅助功能」权限（系统设置 › 隐私与安全性 › 辅助功能）。没有它时对方只能看、不能操作。

## Windows

### 防火墙

第一次开启「允许被远程控制」时，Windows 防火墙会询问是否允许 kkdesk 访问网络。请允许 **专用网络**；如果当前网络被标成「公用」，也要勾上公用网络，否则别的电脑连不进来。

### 端口被系统保留

装了 Hyper-V、WSL 或 Docker 的电脑上，Windows 会整段保留一些端口。kkdesk 的默认端口 21118 落在保留范围里时：

- 先试后面的 20 个端口，都不行就让系统分配一个空闲端口；
- 换到的端口会**保存为默认端口**，下次启动直接用它，本机地址保持不变；
- 连接页会提示「端口 X 被系统保留……已改用 Y，并设为默认端口」。

想自己挑端口，可以先查保留范围，再到「设置 › 网络」改：

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

### 权限限制

以普通权限运行时，对方无法操作「以管理员身份运行」的窗口，也无法操作 UAC 弹窗。

## 自检

被控端出问题时，可以先跑自检，它会检查屏幕采集、编码和输入注入，不需要网络和界面：

```sh
./kkdesk -selftest        # Windows: .\kkdesk.exe -selftest
```

结果写在 `kkdesk-selftest.log`（Windows 上在 `kkdesk.exe` 旁边，macOS 上在当前目录），退出码 0 表示通过。Windows 版还会一直把诊断日志写到同目录的 `kkdesk.log`。反馈问题时请附上这个文件。
