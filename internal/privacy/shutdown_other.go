//go:build !darwin || !cgo

package privacy

func ShutdownOnMain(cleanup func()) { cleanup() }
