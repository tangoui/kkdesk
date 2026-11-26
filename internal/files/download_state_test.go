package files

import (
	"errors"
	"os"
	"testing"
)

func TestDownloadRejectsIncompleteFileSequence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		events      []msg
		fileStarted bool
	}{
		{"mismatched file end", []msg{{Op: "end", Rel: "other.txt"}}, true},
		{"missing file end", nil, true},
		{"second file before end", []msg{{Op: "file", Rel: "other.txt"}}, true},
		{"missing entire file", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dir := newRig(t), t.TempDir()
			root, err := openReceiveRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			tr := r.c.newTransfer(testCtx(t), false, root, nil)
			t.Cleanup(tr.Cancel)
			tr.meter.update(true, func(p *Progress) { p.Files = 1 })
			if tc.fileStarted {
				tr.handle(&msg{Op: "file", Rel: "file.txt"})
			}
			for _, event := range tc.events {
				tr.handle(&event)
			}
			tr.handle(&msg{Op: "done"})
			res, err := tr.Wait()
			if !errors.Is(err, ErrInvalid) || res.Files != 0 {
				t.Errorf("Wait returned %+v, %v, want ErrInvalid and zero completed files", res, err)
			}
			if tc.fileStarted && (len(res.Failed) != 1 || res.Failed[0].Path != "file.txt" || !errors.Is(res.Failed[0].Err, ErrInvalid)) {
				t.Errorf("failed files = %+v, want the unfinished file", res.Failed)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Errorf("destination is not empty: %v, %v", entries, err)
			}
		})
	}
}
