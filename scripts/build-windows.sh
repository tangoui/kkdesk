#!/bin/bash
# Cross-builds mmdesk.exe (windows/amd64, cgo + mingw-w64) for testing on a
# real Windows PC and packages it with a Chinese README.
#
#   scripts/build-windows.sh            # -> /Users/admin/min/outputs/mmdesk-spike/win/<YYYYMMDD>-<NN>/
#   OUT_ROOT=/some/dir scripts/build-windows.sh
#
# The exe is a console-subsystem program on purpose (plain go build, no
# -H windowsgui): the tester sees the "listening ... password ..." line and
# the diagnostics in the PowerShell window, and -selftest prints its result
# there. The Tango main window opens as usual next to the console.
#
# It must depend on system DLLs only (libgcc/libstdc++/winpthread are linked
# statically); the objdump check below enforces that.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out_root=${OUT_ROOT:-/Users/admin/min/outputs/mmdesk-spike/win}
day=$(date +%Y%m%d)
mkdir -p "$out_root"
n=1
while [ -e "$out_root/$day-$(printf %02d $n)" ]; do n=$((n + 1)); done
out="$out_root/$day-$(printf %02d $n)"
mkdir "$out"
log="$out/build.log"

cd "$root"
version=$(git describe --tags --always --dirty 2>/dev/null || echo unknown)
{
	echo "mmdesk Windows build $(date '+%Y-%m-%d %H:%M:%S')"
	echo "version: $version"
	echo "go: $(go version)"
	echo "cc: $(x86_64-w64-mingw32-gcc --version | head -1)"
	echo "cxx: $(x86_64-w64-mingw32-g++ --version | head -1)"
} >"$log"

echo "building $out/mmdesk.exe ($version)"
set +e
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
	go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out/mmdesk.exe" . >>"$log" 2>&1
rc=$?
set -e
if [ $rc -ne 0 ]; then
	cat "$log" >&2
	echo "build failed; see $log" >&2
	exit 1
fi

# Only DLLs that ship with Windows 10/11 are allowed.
system_dlls='^(kernel32|user32|gdi32|advapi32|ole32|oleaut32|shell32|shlwapi|comctl32|comdlg32|uxtheme|dwmapi|imm32|winmm|ws2_32|iphlpapi|dnsapi|bcrypt|crypt32|secur32|userenv|netapi32|ntdll|msvcrt|ucrtbase|version|setupapi|powrprof|avrt|d3d11|d3d12|dxgi|d2d1|dwrite|d3dcompiler_47|mfplat|mf|mfreadwrite|evr|propsys|windowscodecs|winhttp|wininet|shcore|api-ms-win-[a-z0-9-]+)\.dll$'
dlls=$(x86_64-w64-mingw32-objdump -p "$out/mmdesk.exe" | grep 'DLL Name' | awk '{print $3}' | sort -fu)
{
	echo
	echo "imported DLLs:"
	echo "$dlls" | sed 's/^/  /'
} >>"$log"
bad=$(echo "$dlls" | tr 'A-Z' 'a-z' | grep -Ev "$system_dlls" || true)
if [ -n "$bad" ]; then
	echo "non-system DLL dependencies:" >&2
	echo "$bad" >&2
	echo "FAIL: non-system DLLs: $bad" >>"$log"
	exit 1
fi
echo "DLL check: only system DLLs" | tee -a "$log"
echo "$dlls" | sed 's/^/  /'

size=$(stat -f %z "$out/mmdesk.exe" 2>/dev/null || stat -c %s "$out/mmdesk.exe")
sha=$(shasum -a 256 "$out/mmdesk.exe" | awk '{print $1}')
echo "mmdesk.exe: $size bytes, sha256 $sha" >>"$log"

cat >"$out/README.txt" <<EOF
mmdesk Windows 测试版（${version}，$(date +%Y-%m-%d) 构建）
================================================

用途：在 Windows 电脑上共享屏幕，由 Mac 远程查看和控制，测量延迟、帧率和 CPU。
支持 Windows 10 1809 及以上 / Windows 11，64 位（x64）。这一版还没有在真实 Windows 上跑过，
请先做第 2 步自检，把结果发回来。

1) 把本文件夹里的 mmdesk.exe 复制到 Windows 上任意一个目录（例如 D:\\mmdesk）。

2) 自检（不需要网络，不会移动鼠标）：
   在该目录空白处按住 Shift 点右键 →「在此处打开 PowerShell 窗口」（或打开 PowerShell 后 cd 到该目录），运行：
       .\\mmdesk.exe -selftest
   大约 10 秒后结束，最后一行是 SELFTEST PASS 或 SELFTEST FAIL。
   把同目录下生成的 mmdesk-selftest.log 和 selftest.h264 两个文件发回来（无论成功还是失败）。

3) 正式测试（被控端，在 Windows 上）：
       .\\mmdesk.exe -host -log host.jsonl
   首次运行会弹出 Windows 防火墙提示，请选择「允许访问」（勾选「专用网络」即可）。
   如果没弹出或点了取消，Mac 会连不上：到「Windows 安全中心 → 防火墙和网络保护 → 允许应用通过防火墙」里给 mmdesk.exe 勾上「专用」。
   PowerShell 里会打印一行 mmdesk host listening on ... password ......，
   主窗口里也会显示本机 IP 地址和 6 位密码。Mac 连接时 Windows 上会弹出确认，请点「接受」。
   测试期间可以播放视频、拖动窗口；控制模式下试试点击、打字、滚轮。

4) 控制端（在 Mac 上，我来跑也行）：
       mmdesk -connect <Windows 的 IP>:21118 -password <6 位密码> -log view.jsonl

5) 测试完在 Windows 上关闭 mmdesk 窗口（或在 PowerShell 里按 Ctrl+C），
   把同目录下的 host.jsonl 和 mmdesk.log 发回来。

已知限制：
- 以管理员身份运行的窗口、UAC 弹窗、锁屏和 Ctrl+Alt+Del 界面无法被远程控制（Windows 的安全限制）；
  弹出 UAC 时画面可能暂停，关掉后会自动恢复。
- 只共享主显示器；多显示器时其他显示器看不到也点不到。
- 远端画面里不显示 Windows 的鼠标指针（Mac 上看到的是自己的指针）。
- 程序没有数字签名，Windows SmartScreen 可能提示「已保护你的电脑 / 未知发布者」：
  点「更多信息」→「仍要运行」。
- 通过远程桌面（mstsc）连到这台电脑时，屏幕采集和硬件编码可能不可用，请在电脑本机上测试。

文件校验：mmdesk.exe ${size} 字节，SHA-256 ${sha}
EOF
# Windows Notepad copes with LF, but CRLF is safer for older versions.
perl -pi -e 's/\n/\r\n/' "$out/README.txt"

echo "README: $out/README.txt" | tee -a "$log"
echo "out: $out"
