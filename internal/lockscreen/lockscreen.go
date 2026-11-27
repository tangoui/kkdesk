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
)

type Session interface {
	capture.Session
	Done() <-chan struct{}
	Apply(input.Event) error
	Unlock(context.Context, []byte) error
}

type StartFunc func(context.Context, capture.Options, func(capture.Frame)) (Session, error)
