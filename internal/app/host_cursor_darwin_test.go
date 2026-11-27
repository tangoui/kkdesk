//go:build cgo

package app

import (
	"testing"

	"github.com/tangoui/kkdesk/internal/session"
)

func TestDarwinCursorMapping(t *testing.T) {
	var standard [len(darwinCursorShapes)]darwinCursorFingerprint
	for i := range standard {
		standard[i] = darwinCursorFingerprint{image: uint64(i + 1), x: 512, y: 512}
	}
	for i, want := range []session.CursorShape{
		session.CursorDefault, session.CursorPointer, session.CursorText,
		session.CursorNotAllowed, session.CursorResizeEW, session.CursorResizeNS,
		session.CursorGrab, session.CursorGrabbing,
	} {
		if got := matchDarwinCursor(standard[i], standard); got != want {
			t.Errorf("standard cursor %d maps to %v, want %v", i, got, want)
		}
	}
	for _, unknown := range []darwinCursorFingerprint{{}, {image: 200}, {image: 2, x: 1024, y: 512}} {
		if got := matchDarwinCursor(unknown, standard); got != session.CursorDefault {
			t.Errorf("unknown cursor maps to %v, want arrow", got)
		}
	}
}

func TestDarwinSystemCursorFingerprints(t *testing.T) {
	for i, want := range darwinCursorShapes {
		fingerprint, ok := readDarwinCursor(i)
		if !ok && i == 0 {
			t.Skip("standard AppKit cursors unavailable without a graphical session")
		}
		if !ok {
			t.Fatalf("standard cursor %d unavailable", i)
		}
		if got := matchDarwinCursor(fingerprint, darwinStandardCursors()); got != want {
			t.Errorf("standard cursor %d maps to %v, want %v", i, got, want)
		}
	}
}
