package app

import "github.com/tangoui/kkdesk/internal/session"

type cursorPublisher struct {
	last session.CursorShape
	sent bool
}

func (p *cursorPublisher) sample(read func() (session.CursorShape, bool), send func(session.CursorShape) error) {
	shape, ok := read()
	if !ok || (p.sent && p.last == shape) {
		return
	}
	if send(shape) == nil {
		p.last, p.sent = shape, true
	}
}
