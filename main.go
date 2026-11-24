// Command mmdesk is a remote-desktop spike: one binary that runs either as the
// host (screen is shared) or as the viewer (connects and controls).
package main

import (
	"flag"
	"fmt"
	"os"
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

func main() {
	o, fs, err := parseFlags(os.Args[1:])
	if err == flag.ErrHelp {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mmdesk:", err)
		fs.Usage()
		os.Exit(2)
	}
	switch {
	case o.host:
		fmt.Printf("mode: host (fps=%d bitrate=%d autoaccept=%v log=%q)\n", o.fps, o.bitrate, o.autoAccept, o.logPath)
	case o.connect != "":
		fmt.Printf("mode: viewer -> %s (fps=%d bitrate=%d view-only=%v log=%q)\n", o.connect, o.fps, o.bitrate, o.viewOnly, o.logPath)
	default:
		fs.Usage()
	}
}
