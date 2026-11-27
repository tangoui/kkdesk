package app

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/codec"
	"github.com/tangoui/kkdesk/internal/stats"
)

// Version is the build version (git describe), set by main from -ldflags.
var Version = "dev"

// StatsSummaryInterval is how often an active host writes a one-line stats
// summary to the application log.
const StatsSummaryInterval = 10 * time.Second

// OutputDir is where diagnostic files go: next to the executable on
// Windows (users run it from Explorer or a shell anywhere and send the
// folder's files back), falling back to the working directory when that is
// not writable; the working directory elsewhere (go run builds into a
// temporary directory).
func OutputDir() string {
	if runtime.GOOS == "windows" {
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			if f, err := os.CreateTemp(dir, ".kkdesk-probe-*"); err == nil {
				name := f.Name()
				f.Close()
				os.Remove(name)
				return dir
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// AppLog is the human-readable application log: everything written with
// package log goes to stderr and to the file.
type AppLog struct {
	Path string
	mu   sync.Mutex
	f    *os.File
}

// OpenAppLog creates (truncates) path and directs package log to stderr
// plus the file, with millisecond timestamps.
func OpenAppLog(path string) (*AppLog, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	l := &AppLog{Path: path, f: f}
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.SetOutput(fileFirst{file: l, console: os.Stderr})
	return l, nil
}

// fileFirst writes each log line to the file, then to the console, whose
// errors are ignored: a GUI-subsystem build has no usable stderr, and
// io.MultiWriter would stop at it and leave the file empty.
type fileFirst struct {
	file    io.Writer
	console io.Writer
}

func (w fileFirst) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	_, _ = w.console.Write(p)
	return n, err
}

// Write appends p to the file (package log calls it once per line).
func (l *AppLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Write(p)
}

// FileOnly writes one timestamped line to the file only (for lines that
// are already printed to stdout).
func (l *AppLog) FileOnly(format string, args ...any) {
	if l == nil {
		return
	}
	line := time.Now().Format("2006/01/02 15:04:05.000000 ") + fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	l.Write([]byte(line))
}

// Close restores package log to stderr and closes the file.
func (l *AppLog) Close() error {
	if l == nil {
		return nil
	}
	log.SetOutput(os.Stderr)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// SystemInfo returns human-readable lines describing the build, the OS,
// the CPU, the GPUs and the displays (platform part in sysinfo_*.go).
func SystemInfo() []string {
	exe, _ := os.Executable()
	out := []string{
		"kkdesk version: " + Version,
		"time: " + time.Now().Format(time.RFC3339),
		fmt.Sprintf("go: %s %s/%s, %d logical CPUs", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU()),
		"executable: " + exe,
	}
	return append(out, platformInfo()...)
}

// LogSystemInfo writes SystemInfo to package log.
func LogSystemInfo() {
	for _, l := range SystemInfo() {
		log.Printf("sysinfo: %s", l)
	}
}

// EncoderInfo returns the encoder's Info() (hardware/software, MFT or
// VideoToolbox mode), or "" if it has none.
func EncoderInfo(enc codec.Encoder) string {
	if i, ok := enc.(interface{ Info() string }); ok {
		return i.Info()
	}
	return ""
}

// statsSummary formats the latest per-second aggregate and the capture
// counters as one log line.
func statsSummary(s stats.Second, c capture.Stats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "stats: %.1f fps, %.2f Mbps, frames %d", s.FPS, s.BitrateBps/1e6, s.Frames)
	if s.LatencyP50 > 0 {
		fmt.Fprintf(&b, ", capture→sent p50 %s p95 %s", ms(s.LatencyP50), ms(s.LatencyP95))
	}
	keys := make([]string, 0, len(s.Segments))
	for k := range s.Segments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, ", %s %s", k, ms(s.Segments[k]))
	}
	if v, ok := s.CPU["self"]; ok {
		fmt.Fprintf(&b, ", cpu self %.0f%%", v)
	}
	fmt.Fprintf(&b, "; capture complete %d resent %d idle %d other %d reopened %d", c.Complete, c.Resent, c.Idle, c.Other, c.Reopened)
	if c.Complete > 0 && c.AcquireTotal > 0 {
		fmt.Fprintf(&b, " acquire avg %s", ms(c.AcquireTotal/time.Duration(c.Complete)))
	}
	return b.String()
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond)) }
