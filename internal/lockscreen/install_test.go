package lockscreen

import (
	"errors"
	"slices"
	"testing"
)

func TestInstallRollsBackInReverseOrder(t *testing.T) {
	fail := errors.New("cannot start new version")
	var visits []string
	err := installComponent([]installStep{
		{apply: func() error { visits = append(visits, "stop"); return nil }, undo: func() error { visits = append(visits, "restart old"); return nil }},
		{apply: func() error { visits = append(visits, "replace"); return nil }, undo: func() error { visits = append(visits, "restore"); return nil }},
		{apply: func() error { visits = append(visits, "start"); return fail }, undo: func() error { visits = append(visits, "stop failed service"); return nil }},
	})
	if !errors.Is(err, fail) || !slices.Equal(visits, []string{"stop", "replace", "start", "stop failed service", "restore", "restart old"}) {
		t.Fatalf("rollback order %v, error %v", visits, err)
	}
}

func TestInstallAlreadyCurrentLeavesServiceRunning(t *testing.T) {
	n := 0
	ops := installActions{
		stage: func() error { n++; return nil },
		start: func() error { n++; return nil },
	}
	if err := ops.install(true, true, true); err != nil || n != 0 {
		t.Fatalf("current running installation performed %d actions, err %v", n, err)
	}
	if err := ops.install(true, false, true); err != nil || n != 1 {
		t.Fatalf("current stopped installation performed %d actions, err %v", n, err)
	}
}

func TestUninstallCanRetryFileCleanup(t *testing.T) {
	visits := []string{}
	ops := uninstallActions{
		stop:          func() error { visits = append(visits, "stop"); return nil },
		removeService: func() error { visits = append(visits, "remove service"); return nil },
		removeFiles:   func() error { visits = append(visits, "remove files"); return nil },
	}
	if err := ops.uninstall(true); err != nil {
		t.Fatal(err)
	}
	if err := ops.uninstall(false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(visits, []string{"stop", "remove service", "remove files", "remove files"}) {
		t.Fatalf("uninstall steps %v", visits)
	}
}
