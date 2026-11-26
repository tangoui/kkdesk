# kkdesk 锁屏远控阶段 0 · macOS

需要 macOS 14 或更新版本。这里的 `KKDeskLockSpike.app` 是独立探测包，使用临时签名。它记录主显示器截图、接口返回值及进程身份。每次只测试一个身份和一个输入 tap。实际锁屏、系统设置批准和管理员认证均由操作人完成。

## 1. 查看初始状态（约 30 秒）

打开 Terminal，运行以下命令。它们只读，不会注册作业或采集画面。

```bash
cd /Users/admin/min/outputs/kkdesk-lockscreen-spike/mac
./KKDeskLockSpike.app/Contents/MacOS/lockspike --inspect
for kind in daemon agent loginwindow; do ./sm.sh status "$kind"; done
```

在「系统设置 › 隐私与安全性」的「屏幕与系统音频录制」和「辅助功能」里记录当前状态。如需对照授权前后的差异，先在现有状态下跑一轮，再为 `KKDeskLockSpike.app` 授权并重跑。普通进程从 Terminal 启动时，系统也可能按宿主 Terminal 判定授权，以 `preflight` 日志为准。关闭自动填充密码的工具，准备手动解锁。

## 2. 普通进程与 Aqua（每轮约 30–45 秒）

```bash
./probe-after-lock.sh user none
./probe-after-lock.sh aqua none
```

每条命令单独运行。看到提示后手动锁屏。按需唤醒登录界面、选择「使用密码」并点密码框，留空等 **25 秒**。普通进程会在 Terminal 前台自动结束。Aqua 解锁后要回 Terminal 按回车，脚本会收集 `launch.log` 并卸载作业。若解锁时密码框里有测试字符，先全选清空，再输自己的密码。

## 3. root LaunchDaemon 与 LoginWindow LaunchAgent（每轮约 45–60 秒）

```bash
./probe-after-lock.sh daemon none
./probe-after-lock.sh loginwindow none
```

用普通账户在 Terminal 运行。安装及收集、卸载时，`sudo` 可能提示管理员密码，直接在 Terminal 的系统提示中输入。请逐条执行，按上一步方式锁屏、等待、解锁并按回车。若看到 LoginWindow 加载未确认的提示，仍可在 60 秒窗口内锁屏，再按回车收集。以 `probe.log` 中 `manager=LoginWindow` 为运行证据。只出现加载请求或缺少 `probe.log`，记录为「当前锁屏场景未观察到启动」。

## 4. 测输入并查看结果（每轮约 30–60 秒）

对要测试的每个身份，用 `hid`、`session`、`annotated` 依次替换命令中的 `none`。例如：

```bash
./probe-after-lock.sh aqua hid
./probe-after-lock.sh aqua session
./probe-after-lock.sh aqua annotated
```

每一轮须重新锁屏，密码框为空且有焦点。程序在检测到锁屏后等待约 4 秒，尝试三种截图，再投递三个固定字母并截图，不按回车。查看同一采集接口的 `before-*.png` 和 `after-*.png`。前后增加三个圆点才说明输入到达密码框。截图不可读时，可以在解锁前现场记下是否出现三个圆点，随后清空密码框。没有焦点、显示器睡眠和 Touch ID 自动解锁都会影响观测。日志的 `postedPairs` 只记录调用次数。

每轮结果保存在此目录的 `results/<身份>-<tap>.*`，含 `probe.log`、由 launchd 运行时的 `launch.log`，以及成功写出的 PNG。提交结果时附上 macOS 版本、授权项和每轮看到的圆点数，截图中的姓名、通知等个人信息可先遮盖。

## 5. 检查 SMAppService 注册和批准（约 3–5 分钟）

用 Finder 将验证 app 放到 `/Applications`。若 Finder 请求管理员权限，由操作人确认。保留验证 app 的独立名字。注册的三个作业只做身份标记。

```bash
export LOCKSPIKE_APP=/Applications/KKDeskLockSpike.app
for kind in daemon agent loginwindow; do
  ./sm.sh status "$kind"
  ./sm.sh register "$kind"
  ./sm.sh status "$kind"
done
```

如果状态是 `requiresApproval`，到「系统设置 › 通用 › 登录项与扩展」查找验证 app，手动批准。可能出现系统管理员认证。记下批准前后的状态和错误原文。临时签名能否注册由实测确认。若系统要求正式签名，可用项目已有签名身份重建验证 app，构建命令支持 `LOCKSPIKE_SIGN_IDENTITY`。

```bash
for kind in daemon agent loginwindow; do
  ./sm.sh status "$kind"
  ./sm.sh unregister "$kind"
  ./sm.sh status "$kind"
done
```

## 6. 完全撤销（约 1–2 分钟）

普通进程可按 Ctrl+C 停止。Aqua、Daemon、LoginWindow 已经完成自动收集和卸载的轮次可跳过对应命令。手动卸载 Daemon 或 LoginWindow 时可能再次请求管理员密码。

```bash
./uninstall.sh user
./uninstall.sh aqua
./uninstall.sh daemon
./uninstall.sh loginwindow
```

如果系统作业的自动收集失败，先在 Terminal 手动导出日志，再卸载：

```bash
mkdir -m 700 -p results/recovered
sudo /usr/bin/tar -C '/Library/Application Support/com.kkdesk.lockspike/result' -cf - . \
  | /usr/bin/tar -C results/recovered -xf -
```

按上一步注销所有已注册的 SMAppService，再从 `/Applications` 删除测试 app。在系统设置移除本次单独授予的屏幕录制、辅助功能及后台运行权限。提交完验证结果后可删除 `results/` 和整个测试产物目录。
