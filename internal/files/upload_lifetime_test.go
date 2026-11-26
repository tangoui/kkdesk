package files

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestUploadStopsAfterRequestPermissionCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(*rig)
		want error
	}{
		{"revoked", func(r *rig) { r.allowed.Store(false); r.s.CheckAllowed() }, ErrDenied},
		{"closed", func(r *rig) { r.s.ep.close() }, ErrClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("begin", func(t *testing.T) {
				r, dir := newRig(t), t.TempDir()
				tc.stop(r)
				err := r.s.uploadBegin(&msg{Transfer: 1, Dir: dir, Rel: "file.txt"})
				if !errors.Is(err, tc.want) {
					t.Errorf("uploadBegin = %v, want %v", err, tc.want)
				}
				noParts(t, dir)
			})
			t.Run("end", func(t *testing.T) {
				r, dir := newRig(t), t.TempDir()
				if err := r.s.uploadBegin(&msg{Transfer: 1, Dir: dir, Rel: "file.txt"}); err != nil {
					t.Fatal(err)
				}
				tc.stop(r)
				_, err := r.s.uploadEnd(&msg{Transfer: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256(nil))})
				if !errors.Is(err, tc.want) {
					t.Errorf("uploadEnd = %v, want %v", err, tc.want)
				}
				if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
					t.Errorf("destination is not empty: %v, %v", entries, err)
				}
			})
		})
	}
}

func TestUploadStopsBetweenFileOperations(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(*rig)
		want error
	}{
		{"revoked and reenabled", func(r *rig) {
			r.allowed.Store(false)
			r.s.CheckAllowed()
			r.allowed.Store(true)
		}, ErrDenied},
		{"closed", func(r *rig) { r.s.ep.close() }, ErrClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, register := range []bool{true, false} {
				phase := "commit"
				if register {
					phase = "register"
				}
				t.Run(phase, func(t *testing.T) {
					r, dir := newRig(t), t.TempDir()
					root, err := openReceiveRoot(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer root.Close()
					const id = 1
					r.s.mu.Lock()
					epoch := r.s.uploadEpoch
					r.s.mu.Unlock()
					in, err := newIncoming(root, []string{"file.txt"}, 0, 0, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer in.abort()
					if register {
						// The temporary file exists but abortAll cannot see it yet.
						tc.stop(r)
						err = r.s.registerUpload(id, epoch, in)
					} else {
						if err := r.s.registerUpload(id, epoch, in); err != nil {
							t.Fatal(err)
						}
						// Finalization already holds the file, but has not published it.
						tc.stop(r)
						err = r.s.commitUpload(id, epoch, in, func() error {
							t.Error("reached placement after upload stopped")
							return nil
						})
					}
					if !errors.Is(err, tc.want) {
						t.Errorf("%s returned %v, want %v", phase, err, tc.want)
					}
					if r.s.active() != 0 {
						t.Error("upload is still active")
					}
					noParts(t, dir)
				})
			}
		})
	}
}
