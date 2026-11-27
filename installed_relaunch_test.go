package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/tango/sys/autostart"
)

type fakeInstalledApp struct {
	info     lockscreen.ComponentInfo
	startErr error
	starts   []string
}

func (f *fakeInstalledApp) Component() (lockscreen.ComponentInfo, error) { return f.info, nil }

func (f *fakeInstalledApp) StartInstalled(path string) error {
	f.starts = append(f.starts, path)
	return f.startErr
}

func TestInstalledRelaunchUsesMatchingInstalledCopy(t *testing.T) {
	for _, tc := range []struct {
		state     lockscreen.ComponentState
		installed bool
		want      bool
	}{
		{state: lockscreen.ComponentAbsent},
		{state: lockscreen.ComponentNeedsUpdate},
		{state: lockscreen.ComponentRunning, installed: true},
		{state: lockscreen.ComponentRunning, want: true},
		{state: lockscreen.ComponentStopped, want: true},
	} {
		f := &fakeInstalledApp{info: lockscreen.ComponentInfo{State: tc.state, UsingInstall: tc.installed, InstalledPath: "installed/kkdesk.exe"}}
		r := &installedRelaunch{platform: f, pending: filepath.Join(t.TempDir(), "pending")}
		started, err := r.attempt(true)
		if err != nil || started != tc.want || (len(f.starts) == 1) != tc.want {
			t.Errorf("state %v (installed %v): started=%v, starts=%v, err=%v", tc.state, tc.installed, started, f.starts, err)
		}
		if started && f.starts[0] != f.info.InstalledPath {
			t.Errorf("started %q, want %q", f.starts[0], f.info.InstalledPath)
		}
	}
}

func TestInstalledRelaunchStopsAfterUnconfirmedStart(t *testing.T) {
	for _, startErr := range []error{nil, errors.New("CreateProcess failed")} {
		f := &fakeInstalledApp{
			info:     lockscreen.ComponentInfo{State: lockscreen.ComponentRunning, InstalledPath: "installed/kkdesk.exe"},
			startErr: startErr,
		}
		pending := filepath.Join(t.TempDir(), "pending")
		first := &installedRelaunch{platform: f, pending: pending}
		if started, err := first.attempt(true); started != (startErr == nil) || !errors.Is(err, startErr) {
			t.Fatalf("initial launch: started=%v, err=%v", started, err)
		}
		next := &installedRelaunch{platform: f, pending: pending}
		next.confirm()
		if started, err := next.attempt(true); started || !errors.Is(err, errInstalledRelaunchUnconfirmed) || len(f.starts) != 1 {
			t.Fatalf("automatic retry: started=%v, starts=%v, err=%v", started, f.starts, err)
		}
		f.startErr = nil
		if started, err := next.attempt(false); !started || err != nil || len(f.starts) != 2 {
			t.Fatalf("manual retry: started=%v, starts=%v, err=%v", started, f.starts, err)
		}
		f.info.UsingInstall = true
		next.confirm()
		f.info.UsingInstall = false
		if started, err := next.attempt(true); !started || err != nil || len(f.starts) != 3 {
			t.Fatalf("launch after confirmation: started=%v, starts=%v, err=%v", started, f.starts, err)
		}
	}
}

func TestAutomaticInstalledRelaunchSkipsExplicitTestLaunches(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		configured bool
		dev        string
		handoff    bool
		want       bool
	}{
		{want: true},
		{args: []string{autostart.LoginArg}, want: true},
		{configured: true},
		{args: []string{"-host"}},
		{args: []string{"-selftest"}},
		{args: []string{"-applog", "test.log"}},
		{args: []string{"-fps", "30"}},
		{dev: "worker"},
		{handoff: true},
	} {
		if got := wantsAutomaticInstalledRelaunch(tc.args, tc.configured, tc.dev, tc.handoff); got != tc.want {
			t.Errorf("args=%v configured=%v dev=%q handoff=%v: got %v, want %v", tc.args, tc.configured, tc.dev, tc.handoff, got, tc.want)
		}
	}
}
