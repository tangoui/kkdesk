package main

// Command-line flags. Without flags kkdesk opens its main window (tan dev).

import (
	"flag"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/tangoui/kkdesk/internal/app"
)

type options struct {
	host       bool
	connect    string
	password   string
	fps        int
	maxWidth   int
	bitrate    int
	logPath    string
	autoAccept bool
	viewOnly   bool
	selftest   bool
	appLog     string
}

func parseFlags(args []string) (options, *flag.FlagSet, error) {
	var o options
	fs := flag.NewFlagSet("kkdesk", flag.ContinueOnError)
	fs.BoolVar(&o.host, "host", false, "run as host: share this screen and accept connections")
	fs.StringVar(&o.connect, "connect", "", "run as viewer: connect to the host at `addr`")
	fs.StringVar(&o.password, "password", "", "temporary password of the host (viewer)")
	fs.IntVar(&o.fps, "fps", 60, "frame rate: 30, 60, 90 or 144")
	fs.IntVar(&o.maxWidth, "maxwidth", 1920, "host: scale the captured screen down to at most this width (0 = native)")
	fs.IntVar(&o.bitrate, "bitrate", 0, "bitrate in Mbps (0 = auto)")
	fs.StringVar(&o.logPath, "log", "", "write JSON Lines stats to `file`")
	fs.BoolVar(&o.autoAccept, "autoaccept", false, "host: accept incoming connections without asking (measurement)")
	fs.BoolVar(&o.viewOnly, "view-only", false, "viewer: do not send input")
	fs.BoolVar(&o.selftest, "selftest", false, "check capture, encoding and input injection without UI or network, write "+app.SelftestLog+" and "+app.SelftestH264+", then exit (0 = pass)")
	fs.StringVar(&o.appLog, "applog", defaultAppLog(), "write a human-readable diagnostic log to `file` (\"\" = none)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  kkdesk -selftest\n  kkdesk -host [-autoaccept] [-fps N] [-bitrate Mbps] [-log file]\n  kkdesk -connect addr -password pw [-view-only] [-log file]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return o, fs, err
	}
	if o.host && o.connect != "" {
		return o, fs, fmt.Errorf("-host and -connect are mutually exclusive")
	}
	return o, fs, nil
}

// defaultAppLog is kkdesk.log in app.OutputDir on Windows (users send it
// back after a test), none elsewhere.
func defaultAppLog() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(app.OutputDir(), "kkdesk.log")
	}
	return ""
}
