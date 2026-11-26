#!/bin/bash
set -euo pipefail
umask 077
base=$(cd "$(dirname "$0")" && pwd)
source "$base/plist.sh"
app="$base/KKDeskLockSpike.app"
binary=Contents/MacOS/lockspike
uid=$(id -u)
rootdir='/Library/Application Support/com.kkdesk.lockspike'
action=${1:-}
mode=${2:-}
label="com.kkdesk.lockspike.legacy.$mode"
session=''
case "$mode" in
    user) ;;
    aqua)
        domain="gui/$uid"
        jobdir="$HOME/Library/Application Support/com.kkdesk.lockspike"
        plist="$jobdir/$label.plist"
        session=Aqua
        ;;
    daemon)
        domain=system
        jobdir="$rootdir"
        plist="/Library/LaunchDaemons/$label.plist"
        ;;
    loginwindow)
        jobdir="$rootdir"
        plist="/Library/LaunchAgents/$label.plist"
        session=LoginWindow
        ;;
    *)
        printf '用法：install.sh user|aqua|daemon|loginwindow hid|session|annotated|none 结果目录\n'
        printf '      uninstall.sh user|aqua|daemon|loginwindow\n'
        exit 2
        ;;
esac

if [[ $action == uninstall ]]; then
    case "$mode" in
        user) exit 0 ;;
        aqua)
            if launchctl print "$domain/$label" >/dev/null 2>&1; then
                launchctl bootout "$domain/$label"
            fi
            rm -f "$plist"
            rmdir "$jobdir" 2>/dev/null || true
            ;;
        daemon|loginwindow)
            sudo -v
            if [[ $mode == daemon ]] && launchctl print "$domain/$label" >/dev/null 2>&1; then
                sudo /bin/launchctl bootout "$domain/$label"
            elif [[ $mode == loginwindow && -f $plist ]]; then
                sudo /bin/launchctl unload -S LoginWindow "$plist" || true
            fi
            sudo /bin/rm -f "$plist"
            if [[ ! -e "/Library/LaunchDaemons/com.kkdesk.lockspike.legacy.daemon.plist" &&
                  ! -e "/Library/LaunchAgents/com.kkdesk.lockspike.legacy.loginwindow.plist" ]]; then
                sudo /bin/rm -rf "$rootdir"
            fi
            ;;
    esac
    exit 0
fi

[[ $action == install && $# == 4 && $uid != 0 ]] || exit 2
tap=$3
dest=$(cd "$4" && pwd)
case "$tap" in hid|session|annotated|none) ;; *) exit 2 ;; esac
[[ -x "$app/$binary" && ! -e "$dest/probe.log" ]] || exit 2

if [[ $mode == user ]]; then
    expires=$(($(date +%s) + 60))
    exec "$app/$binary" --watch "$mode" "$tap" "$dest" "$expires" >"$dest/probe.log" 2>&1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
staged="$work/$label.plist"
if [[ $mode == aqua ]]; then
    mkdir -p "$jobdir"
    [[ ! -e "$plist" ]] || { printf '请先卸载上次的 Aqua 作业。\n' >&2; exit 1; }
    expires=$(($(date +%s) + 60))
    make_job_plist "$staged" "$label" Program "$app/$binary" "$session" "$dest/probe.log" \
        lockspike --watch "$mode" "$tap" "$dest" "$expires"
    install -m 0600 "$staged" "$plist"
    launchctl bootstrap "$domain" "$plist"
    exit 0
fi

# All paths written by the privileged jobs are under a freshly created root-only directory.
sudo -v
[[ ! -e "$plist" ]] || { printf '请先卸载上次的系统作业。\n' >&2; exit 1; }
sudo /bin/mkdir -m 0700 "$rootdir"
sudo /usr/bin/ditto --noacl "$app" "$rootdir/KKDeskLockSpike.app"
sudo /usr/sbin/chown -R root:wheel "$rootdir"
sudo /bin/chmod -R go-w "$rootdir"
sudo /usr/bin/codesign --verify --strict "$rootdir/KKDeskLockSpike.app"
sudo /bin/mkdir -m 0700 "$rootdir/result"
expires=$(($(date +%s) + 60))
make_job_plist "$staged" "$label" Program "$rootdir/KKDeskLockSpike.app/$binary" "$session" "$rootdir/result/probe.log" \
    lockspike --watch "$mode" "$tap" "$rootdir/result" "$expires"
sudo /usr/bin/install -o root -g wheel -m 0644 "$staged" "$plist"
if [[ $mode == daemon ]]; then
    sudo /bin/launchctl bootstrap "$domain" "$plist"
else
    # This is a request to launchd, not evidence of a LoginWindow process.
    sudo /bin/launchctl load -S LoginWindow "$plist"
fi
