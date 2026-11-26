#!/bin/bash
set -euo pipefail
umask 077
base=$(cd "$(dirname "$0")" && pwd)
mode=${1:-user}
tap=${2:-none}
case "$mode" in user|aqua|daemon|loginwindow) ;; *) exit 2 ;; esac
case "$tap" in hid|session|annotated|none) ;; *) exit 2 ;; esac
mkdir -p "$base/results"
dest=$(mktemp -d "$base/results/$mode-$tap.XXXXXX")
printf '结果目录：%s\n' "$dest"

if [[ $mode == user ]]; then
    printf '现在请手动锁屏，保持登录界面约 25 秒后解锁。\n'
    exec "$base/install.sh" "$mode" "$tap" "$dest"
fi
if [[ $mode == daemon || $mode == loginwindow ]]; then
    sudo -v
fi
install_status=0
if "$base/install.sh" "$mode" "$tap" "$dest" >"$dest/launch.log" 2>&1; then
    printf '现在请手动锁屏，保持登录界面约 25 秒后解锁。然后在此按回车收集结果。\n'
    read -r _
else
    install_status=$?
    printf 'launchd 加载未确认，已保留日志。仅 LoginWindow 仍可手动锁屏验证是否由系统自动启动。\n' >&2
    if [[ $mode == loginwindow ]]; then read -r _; fi
fi
case "$mode" in
    aqua) launchctl print "gui/$(id -u)/com.kkdesk.lockspike.legacy.aqua" >>"$dest/launch.log" 2>&1 || true ;;
    daemon|loginwindow)
        root='/Library/Application Support/com.kkdesk.lockspike/result'
        if [[ $mode == daemon ]]; then
            launchctl print system/com.kkdesk.lockspike.legacy.daemon >>"$dest/launch.log" 2>&1 || true
        fi
        if ! sudo /usr/bin/tar -C "$root" -cf - . | /usr/bin/tar -C "$dest" -xf -; then
            printf '结果提取失败，先保留系统目录并查看 README 的恢复命令。\n' >&2
            exit 1
        fi
        ;;
esac
"$base/uninstall.sh" "$mode"
printf '结果目录：%s\n' "$dest"
exit "$install_status"
