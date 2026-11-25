package appui

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"sync"
)

// The kkdesk mark (docs/design/logo/mark-3.svg, rendered at 96 px for the
// 24 pt brand slot on Retina screens).
//
//go:embed assets/mark.png
var markPNG []byte

var (
	markOnce sync.Once
	markImg  image.Image
)

// brandMark returns the decoded mark, or nil if it cannot be decoded.
func brandMark() image.Image {
	markOnce.Do(func() {
		if img, err := png.Decode(bytes.NewReader(markPNG)); err == nil {
			markImg = img
		}
	})
	return markImg
}
