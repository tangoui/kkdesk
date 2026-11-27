//go:build !windows && (!darwin || !cgo)

package app

import "github.com/tangoui/kkdesk/internal/session"

func hostCursor() (session.CursorShape, bool) { return session.CursorDefault, false }
