//go:build windows && cgo

package codec

// NewDecoder is not implemented on Windows yet (Windows is only the
// controlled side in this step).
func NewDecoder(out DecodeOutput) (Decoder, error) {
	return nil, ErrUnsupported
}
