// Package lockscreen connects the host to an optional local lock screen component.
package lockscreen

import (
	"context"
	"errors"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/input"
)

var (
	ErrUnavailable    = errors.New("lock screen component unavailable")
	ErrStillLocked    = errors.New("desktop still locked")
	ErrNeedsSignature = errors.New("不可用（需要正式签名的 kkdesk）")
	ErrNotInstalled   = errors.New("lock screen component is not installed")
	ErrWrongLocation  = errors.New("kkdesk is running outside the lock screen component's installation")
	ErrService        = errors.New("lock screen service or helper unavailable")
	ErrNeedsUpdate    = errors.New("lock screen component version mismatch")
	ErrNeedsApproval  = errors.New("lock screen component requires system approval")
	ErrUnsupported    = errors.New("lock screen component is unsupported on this system")
)

type Session interface {
	capture.Session
	Done() <-chan struct{}
	Apply(input.Event) error
	Unlock(context.Context, []byte) error
}

type StartFunc func(context.Context, capture.Options, func(capture.Frame)) (Session, error)
