package main

// Command-line flags. Without flags mmdesk opens its main window (tan dev).

import (
	"flag"
	"fmt"
)

type options struct {
	host       bool
	connect    string
	password   string
	fps        int
	bitrate    int
	logPath    string
	autoAccept bool
	viewOnly   bool
}

func parseFlags(args []string) (options, *flag.FlagSet, error) {
	var o options
	fs := flag.NewFlagSet("mmdesk", flag.ContinueOnError)
	fs.BoolVar(&o.host, "host", false, "run as host: share this screen and accept connections")
	fs.StringVar(&o.connect, "connect", "", "run as viewer: connect to the host at `addr`")
	fs.StringVar(&o.password, "password", "", "temporary password of the host (viewer)")
	fs.IntVar(&o.fps, "fps", 60, "frame rate: 30, 60, 90 or 144")
	fs.IntVar(&o.bitrate, "bitrate", 0, "bitrate in Mbps (0 = auto)")
	fs.StringVar(&o.logPath, "log", "", "write JSON Lines stats to `file`")
	fs.BoolVar(&o.autoAccept, "autoaccept", false, "host: accept incoming connections without asking (measurement)")
	fs.BoolVar(&o.viewOnly, "view-only", false, "viewer: do not send input")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  mmdesk -host [-autoaccept] [-fps N] [-bitrate Mbps] [-log file]\n  mmdesk -connect addr -password pw [-view-only] [-log file]\n\nFlags:\n")
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
