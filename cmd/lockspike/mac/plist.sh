# Sourced by build.sh and jobs.sh.
make_job_plist() {
    local plist=$1 label=$2 executable_key=$3 executable=$4 session=$5 logfile=$6
    shift 6
    rm -f "$plist"
    plutil -create xml1 "$plist"
    plutil -insert Label -string "$label" "$plist"
    plutil -insert "$executable_key" -string "$executable" "$plist"
    plutil -insert RunAtLoad -bool true "$plist"
    if [[ -n $session ]]; then
        plutil -insert LimitLoadToSessionType -string "$session" "$plist"
    fi
    if [[ -n $logfile ]]; then
        plutil -insert StandardOutPath -string "$logfile" "$plist"
        plutil -insert StandardErrorPath -string "$logfile" "$plist"
    fi
    plutil -insert ProgramArguments -array "$plist"
    local arg index=0
    for arg in "$@"; do
        plutil -insert "ProgramArguments.$index" -string "$arg" "$plist"
        index=$((index + 1))
    done
    plutil -lint "$plist"
}
