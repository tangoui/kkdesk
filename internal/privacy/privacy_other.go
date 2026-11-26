//go:build (!darwin && !windows) || !cgo

package privacy

func NewScreen() Screen { return unsupportedScreen{} }
