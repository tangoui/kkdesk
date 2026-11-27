//go:build !windows && (!darwin || !cgo)

package unlockcred

func nativeStore(string) Store { return nil }
