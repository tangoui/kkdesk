package privacy

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestLockRequiresSystemConfirmation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var requested bool
		lock := confirmedLocker{
			request: func() error { requested = true; return nil },
			locked:  func() (bool, error) { return requested && time.Since(start) >= 300*time.Millisecond, nil },
		}
		if err := lock.Lock(); err != nil || time.Since(start) != 300*time.Millisecond {
			t.Fatalf("Lock = %v after %s", err, time.Since(start))
		}
	})
}

func TestLockConfirmationTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		lock := confirmedLocker{request: func() error { return nil }, locked: func() (bool, error) { return false, nil }}
		if err := lock.Lock(); !errors.Is(err, errLockTimeout) || time.Since(start) != 5*time.Second {
			t.Fatalf("Lock = %v after %s, want timeout after 5s", err, time.Since(start))
		}
	})
}

func TestLockRequestFailure(t *testing.T) {
	fail := errors.New("platform refused lock request")
	lock := confirmedLocker{
		request: func() error { return fail },
		locked:  func() (bool, error) { t.Error("polled after request failure"); return false, nil },
	}
	if err := lock.Lock(); !errors.Is(err, fail) {
		t.Fatalf("Lock = %v, want %v", err, fail)
	}
}

func TestLockTimeoutIncludesBlockedNativeCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		start := time.Now()
		lock := confirmedLocker{
			request: func() error { <-release; return nil },
			locked:  func() (bool, error) { return true, nil },
		}
		if err := lock.Lock(); !errors.Is(err, errLockTimeout) || time.Since(start) != 5*time.Second {
			t.Fatalf("Lock = %v after %s, want timeout after 5s", err, time.Since(start))
		}
	})
}
