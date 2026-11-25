# 发版

kkdesk 的发布和自动更新用的是 Tango 的 `service/updater`：每个 GitHub Release 带一份签名清单 `latest.json`，客户端从 `https://github.com/tangoui/kkdesk/releases/latest/download/latest.json` 获取最新版本（`internal/update`）。

## 发布一个版本

1. 把 `tan.json` 里的 `"version"` 改成新版本号，例如 `0.1.1`，可选地写一份更新说明 `docs/releases/0.1.1.md`（会显示在更新窗口和 Release 页面上）。
2. 合并到 main 后打标签并推送：

   ```sh
   git tag v0.1.1
   git push origin v0.1.1
   ```

3. GitHub Actions 的 **Release** 工作流（`.github/workflows/release.yml`）会：
   - 检查标签和 `tan.json` 的版本一致；
   - 构建 macOS（Apple 芯片、Intel，临时签名）、Windows（x64）、Linux（x64、arm64）客户端和 `kkdesk-server`；
   - 下载上一个 Release 的文件，生成到新版本的二进制补丁；
   - 用发布密钥签名 `latest.json`，连同所有文件和 `SHA256SUMS` 发布成 GitHub Release。

发布后，已安装的客户端在 24 小时内会检查到新版本。

## 产物

| 文件 | 内容 |
|---|---|
| `latest.json` | 签名的更新清单 |
| `kkdesk-<版本>-darwin-arm64.app.tar.gz`、`…-darwin-amd64.app.tar.gz` | macOS 应用 |
| `kkdesk-<版本>-windows-amd64.exe` | Windows 程序 |
| `kkdesk-<版本>-linux-amd64.tar.gz`、`…-linux-arm64.tar.gz` | Linux 程序目录 |
| `kkdesk-<旧版本>-to-<版本>-<平台>.patch` | 增量补丁 |
| `kkdesk-server-<版本>-linux-amd64.tar.gz`、`…-linux-arm64.tar.gz` | 跨网服务 |
| `SHA256SUMS` | 所有文件的 SHA-256 |

## 密钥

仓库只需要一个 Actions 密钥 `TANGO_UPDATE_KEY`：更新签名私钥（`tan keygen` 生成），公钥写在 `internal/update/update.go`。

::: danger 更新私钥丢了或泄露了
私钥丢失后，已安装的客户端再也收不到更新；泄露后别人可以给你的用户推送伪造的更新。私钥要离线备份，不要放进仓库。需要换钥匙时，先发一个同时信任新旧公钥（`Updater.PublicKeys`）的版本，再改用新钥匙签名。
:::

### macOS 代码签名

macOS 包目前是**临时签名**（`codesign --sign -`），不需要证书。代价是 macOS 按签名身份记住「屏幕录制」「辅助功能」权限，临时签名每个版本都不同，所以用户每次更新后要重新授权；第一次打开也要在「隐私与安全性」里点「仍要打开」。

以后有了 Developer ID 证书，可以在工作流里导入证书、改用 `tan sign`（加 `-notarize` 公证）。注意：一旦发布过带 Team ID 的版本，之后的版本必须用同一个 Team ID 签名，更新器会拒绝 Team ID 不一致的新版本。

## 清单定期重签

清单默认 30 天后过期，过期后客户端会拒绝。**Re-sign update manifest** 工作流（`.github/workflows/resign-update.yml`）每周一自动重签最新 Release 的 `latest.json`（版本和文件不变）。也可以在 Actions 页面手动运行。

## 发版前演练

在 Mac 上完整演练一次自动更新：

```sh
scripts/rehearse-update.sh
```

脚本会构建并签名两个版本（0.0.1 和 0.0.2，用 0.0.x 是为了不影响真实版本），用发布密钥签名 0.0.2 的清单（包含从 0.0.1 的补丁），在 `127.0.0.1:8765` 上提供下载，然后运行端到端测试 `TestE2EUpdate`：0.0.1 在「设置 › 通用」里检查更新、下载、安装并重启到 0.0.2，新版本确认启动成功。需要本机有更新私钥（默认 `~/.config/kkdesk-release/update.key`）。应用和发版一样临时签名；设置 `TANGO_SIGN_IDENTITY` 可以改用指定的证书签名。

## 本地测试其他清单

环境变量 `KKDESK_UPDATE_URL` 可以把清单地址换成本地服务器（只接受 http/https），签名仍然用内置公钥校验。`KKDESK_NO_UPDATE` 关闭自动更新。
