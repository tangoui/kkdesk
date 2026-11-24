//go:build !(darwin || windows) || !cgo

package codec

// NewEncoder is not implemented on this platform.
func NewEncoder(w, h, fps, bps int, out EncodeOutput) (Encoder, error) {
	return nil, ErrUnsupported
}

// NewDecoder is not implemented on this platform.
func NewDecoder(out DecodeOutput) (Decoder, error) {
	return nil, ErrUnsupported
}
