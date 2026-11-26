//go:build !darwin || !cgo

package privacy

func NewScreen() Screen { return unsupportedScreen{} }
