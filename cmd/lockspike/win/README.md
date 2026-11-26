# kkdesk 锁屏远控阶段 0 · Windows

需要 Windows 10 或 11 x64、实体或 VM 的本地控制台、能手动锁定和解锁的账户，以及管理员权限。请用本机控制台操作。验证程序使用单独的按需启动服务 `KKDeskLockSpike`，把测试 exe、日志及截图放在受保护的 `%ProgramFiles%\KKDeskLockSpike`。辅助进程只在测试身份为 LocalSystem、会话已锁定且输入桌面名为 Winlogon 时探测。

## 1. 校验文件与安装（约 1–2 分钟）

在 Windows 打开 PowerShell，切换到放置 `kkdesk-lockspike.exe` 的目录，查看 SHA-256 并与随包的 `SHA256SUMS` 比对。

```powershell
Get-FileHash .\kkdesk-lockspike.exe -Algorithm SHA256
```

以管理员身份打开 PowerShell，仍在同一目录运行：

```powershell
.\kkdesk-lockspike.exe -spike-service-install
```

打开管理员 PowerShell 时可能出现 UAC 确认或管理员密码提示，请直接在系统提示中处理。程序没有发布签名。安装命令会启动一次性验证流程，等待两分钟内出现锁屏。

## 2. 锁屏并解锁（约 20–30 秒）

看到 `Probe armed` 后按 **Win+L**。立即在锁屏界面按空格或点击，进入登录页，必要时在「登录选项」选密码，让空密码框保持焦点。等至少 **10 秒**，建议等 **20 秒**留出采集时间。屏幕保持唤醒。探测只尝试投递固定的三个字母，不按回车。观察密码框是否增加三个圆点。清空密码框后用自己的密码手动解锁。

## 3. 收集结果（约 1 分钟）

在管理员 PowerShell 中运行：

```powershell
$probe = Join-Path $env:ProgramFiles KKDeskLockSpike
Get-ChildItem $probe
$report = Join-Path $env:TEMP ('KKDeskLockSpike-' + (Get-Date -Format yyyyMMdd-HHmmss))
New-Item -ItemType Directory -Path $report | Out-Null
Get-ChildItem $probe -File | Where-Object { $_.Extension -in '.log','.png' } |
  Copy-Item -Destination $report
Write-Output $report
```

回传 `service.log`、有则回传 `helper.log` 和 `before.png` / `after.png`。附 Windows 版本和现场看到的圆点数，可先遮盖截图中的姓名等个人信息。`SendInput sent=6/6` 表示系统接受了三对按键事件。确认送达需要比较密码圆点，或由现场操作人观察。若只有 `service.log`，它会记录等待锁屏、启动助手或超时的错误。

## 4. 卸载（约 30 秒）

收集结果后，在原始 exe 所在目录执行：

```powershell
.\kkdesk-lockspike.exe -spike-service-uninstall
sc.exe query KKDeskLockSpike
Test-Path (Join-Path $env:ProgramFiles KKDeskLockSpike)
```

预期服务不存在，且 `Test-Path` 返回 `False`。如需复测，先完成这一步再重新安装。结果发送完毕后可删除临时结果目录和测试 exe。
