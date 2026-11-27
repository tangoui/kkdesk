# 发版安装包设计

日期：2026-11-27
状态：用户已同意（"按你建议，自审没问题就派 codex 干"）

## 为什么

v0.1.0-beta.2 的 Release 里，Mac 只有 `.app.tar.gz`，Windows 只有裸的 `kkdesk.exe`，Linux 只有 `.tar.gz`。用户要的是安装包：Mac 是 dmg，Windows 是安装程序。

## 原则

- **安装包给首次安装的用户，更新包继续给自动更新用。** `latest.json` 引用的 `.app.tar.gz`、`.exe`、`.tar.gz` 与二进制补丁保持不变，继续作为 Release 资产；新增安装包资产。发版说明和 README 把下载指引改为安装包。
- 安装包里的程序与更新包里的完全一样（同一次构建、同样的签名和附带文件）。

## A. macOS：dmg

- 在 `release.yml` 的 macos 作业里，在现有"构建 → `bundle-macos-lockscreen.sh` 加锁屏组件 plist → ad hoc 签名 → 校验"之后，用**这个已签名的 app** 生成 dmg：打开后显示 kkdesk.app 和"应用程序"快捷方式，拖拽安装（`tan package` 的 dmg 布局，或 `hdiutil` + 应用程序符号链接，实现者选；不能重新构建出一个不带锁屏 plist 的 app）。
- 两个架构各一个：`kkdesk-<版本>-darwin-arm64.dmg`、`kkdesk-<版本>-darwin-amd64.dmg`（命名风格与现有资产一致）。
- dmg 本身做 ad hoc 签名即可（没有 Developer ID，不公证）；发版说明保留"第一次打开需要在隐私与安全性里点仍要打开"。

## B. Linux：AppImage 与 deb

- 在 linux 作业里用 `tan package -format tar,appimage,deb`（或等价）额外产出 AppImage 和 `.deb`，两个架构都要。现有 `.tar.gz`（更新用）保留。
- 若 arm64 某种格式在 CI 上做不了，如实跳过并在汇报里说明，不要硬凑。

## C. Windows：NSIS 安装程序（按用户安装）

**选择：按用户安装**（tan 的默认 scope），装到 `%LOCALAPPDATA%\Programs\kkdesk`，不需要管理员权限。理由：
- 自动更新直接覆盖用户目录下的 exe，不用提权；
- 不与锁屏远控组件抢 `%ProgramFiles%\kkdesk`：组件安装时照旧把 exe 复制到 Program Files 并注册服务，启动时照旧自动切到 Program Files 那份（现有逻辑）。

需要实现者核对并处理的交互：
1. **开始菜单 / 桌面快捷方式** 指向 `%LOCALAPPDATA%\Programs\kkdesk\kkdesk.exe`。装了锁屏组件后，从快捷方式启动会按现有"自动切到安装位置"逻辑切到 Program Files 那份——确认这条路径在安装程序场景下正常（版本一致才切，防循环保护有效）。
2. **自动更新与锁屏组件**：装了组件后实际运行的是 Program Files 那份，更新器会尝试更新正在运行的 exe。读 `internal/update` 与 Tango updater 的 Windows 安装方式，弄清这时会发生什么（失败？提权？只更新了用户目录那份导致版本不一致？），并选一个让用户能顺利更新的做法：例如更新始终作用于用户目录那份，更新后组件显示"需要更新"、由用户在设置里一键更新组件（现有流程）。写进 `docs/guide/update.md`。如果需要改代码，先写失败测试。
3. **卸载**：卸载程序出现在"设置 › 应用"里。若检测到锁屏远控组件已安装，卸载时提示并提供一并卸载（需要管理员确认，调用现有 `kkdesk.exe -uninstall-service` 流程），用户拒绝则只卸载用户目录部分并说明组件仍在。卸载不删除用户配置目录 `%APPDATA%\kkdesk`（与常见做法一致；如 tan 的 NSIS 模板有选项，默认保留）。
4. **安装完成后启动 kkdesk**（tan 的 runAfterInstall，若支持）。
5. 资产名：`kkdesk-<版本>-windows-amd64-setup.exe`。原 `kkdesk-<版本>-windows-amd64.exe`（更新用）保留。

## D. 工具版本

`release.yml` 固定的 `TAN_VERSION`（目前 v0.2.1）可能不支持上述打包。需要时升级到已发布的 Tango 版本（如 v0.4.0），并在本地用同版本试打包；升级后确认现有构建步骤、`tan release-manifest` 行为不变。

## E. 文档

- `docs/guide/install.md`：下载表改为 dmg / setup.exe / AppImage·deb，写清 Mac 拖拽安装与首次打开、Windows 按用户安装（无需管理员）、锁屏组件另需管理员。
- README 三种语言的"开始使用"一节同步（英、中、日）。
- `docs/releases/` 下下一个版本的说明里写明新增安装包。

## 验证

- 在 CI 之外不能真正跑 Release；用 `workflow_dispatch` 或在分支上新增一个**不发布**的打包检查作业（只上传 artifact）验证 dmg/AppImage/deb/setup.exe 都能产出（实现者选，不要打 tag、不要发布 Release）。
- 本机：在 Mac 上打出 dmg，挂载确认内容（app + 应用程序链接、app 内有锁屏 plist、签名校验通过）；Windows 安装程序只能交叉打包，交给用户在 Windows 上实测，附中文实测步骤（安装、启动、装锁屏组件后从快捷方式启动、自动更新、卸载含组件）。
- 现有 CI 必须保持通过。

## 我替用户做的决定

1. Windows 按用户安装（不需要管理员），锁屏组件仍单独装到 Program Files。
2. 更新包保留，安装包为新增资产。
3. 卸载时询问是否一并卸载锁屏组件；默认保留用户配置。
4. 不做 MSIX（需要证书）。
