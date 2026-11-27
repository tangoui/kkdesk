# macOS 锁屏远程操作实测

准备一台 macOS 13 或更新版本的被控 Mac、另一台电脑上的同版本 kkdesk，以及能在被控 Mac 本机手动解锁的账户。请保留现场操作人员，直到确认可以恢复画面、输入和正常桌面。

## 构建与签名

如果使用从源码生成的测试包，先确认 app 中包含守护进程和助手的清单。以下命令从 kkdesk 项目目录运行，构建当前架构并加入清单。

```sh
MACOSX_DEPLOYMENT_TARGET=13.0 \
  CGO_CFLAGS="-O2 -g -mmacosx-version-min=13.0" \
  CGO_CXXFLAGS="-O2 -g -mmacosx-version-min=13.0" \
  CGO_LDFLAGS="-O2 -g -mmacosx-version-min=13.0" \
  tan build -platform "darwin/$(go env GOARCH)" -o build/macos-lockscreen
bash scripts/bundle-macos-lockscreen.sh build/macos-lockscreen/darwin-*/kkdesk.app
```

使用项目已有的 Apple 开发者身份签名该 app，然后验证 bundle。将命令中的路径替换为包里的实际路径。

```sh
tan sign -identity "$KKDESK_CODESIGN_IDENTITY" /path/to/kkdesk.app
codesign --verify --deep --strict /path/to/kkdesk.app
codesign -dv --verbose=4 /path/to/kkdesk.app
```

查看 `TeamIdentifier` 是否符合预期。临时签名的包打开安全设置时应显示「不可用（需要正式签名的 kkdesk）」。

## 安装与批准

1. 将签名后的测试 app 放到 `/Applications`，退出旧版后打开它。
2. 在「设置 › 安全 › 锁屏时允许远程操作」点「安装」。如果显示等待系统批准，在「系统设置 › 通用 › 登录项与扩展」批准 kkdesk，回到应用点「刷新」。管理员认证由现场操作人员完成。
3. 核对「隐私与安全性」里的「录屏与系统音频」和「辅助功能」。按系统提示授权 kkdesk，授权后按提示重启应用。

## 第一项实机验证

从另一台电脑建立普通远控会话，在被控 Mac 上手动锁屏。先确认控制端能看到登录界面，并能用远程鼠标或键盘让密码框获得焦点。使用空密码框验证少量测试字符是否出现，随后清空。若画面停住或密码框没有变化，现场手动解锁，记录 macOS 版本、授权状态和具体表现，再继续测试。

## 解锁、恢复与卸载

1. 两项基础能力通过后，重新锁屏，从控制中心选择「解锁电脑…」。输入被控 Mac 的账户密码。确认系统解锁后画面切回桌面，会话保持连接。
2. 在可恢复测试账户上验证保存、忘记和密码不正确的提示。确认「仅查看」隐藏解锁项。测试复连时按现有连接批准策略操作。
3. 在安全设置卸载组件，回到「登录项与扩展」确认 kkdesk 后台组件已撤销。再建立会话并锁屏，核对控制端显示组件不可用。现场解锁后，按需撤销本轮授予的屏幕录制和辅助功能权限。

诊断时可在 Console 中按 `kkdesk lock screen` 筛选，附上系统版本与状态说明。请勿回传账户密码或 PIN。
