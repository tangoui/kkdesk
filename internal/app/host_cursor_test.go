package app

import (
	"errors"
	"slices"
	"testing"

	"github.com/tangoui/kkdesk/internal/session"
)

func TestHostCursorPublishesFirstShapeAndChanges(t *testing.T) {
	var publisher cursorPublisher
	var sent []session.CursorShape
	send := func(shape session.CursorShape) error {
		sent = append(sent, shape)
		return nil
	}
	for _, sample := range []struct {
		shape session.CursorShape
		ok    bool
	}{
		{session.CursorDefault, false},
		{session.CursorDefault, true},
		{session.CursorDefault, true},
		{session.CursorPointer, true},
		{session.CursorDefault, false},
		{session.CursorPointer, true},
		{session.CursorDefault, true},
	} {
		publisher.sample(func() (session.CursorShape, bool) { return sample.shape, sample.ok }, send)
	}
	want := []session.CursorShape{session.CursorDefault, session.CursorPointer, session.CursorDefault}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent %v, want %v", sent, want)
	}
}

func TestHostCursorRetriesFailedSend(t *testing.T) {
	var publisher cursorPublisher
	read := func() (session.CursorShape, bool) { return session.CursorText, true }
	attempts := 0
	send := func(session.CursorShape) error {
		attempts++
		if attempts == 1 {
			return errors.New("channel unavailable")
		}
		return nil
	}
	for range 3 {
		publisher.sample(read, send)
	}
	if attempts != 2 {
		t.Fatalf("%d sends, want one failed attempt and one successful retry", attempts)
	}
}
