package termimage

import (
	"encoding/base64"
	"fmt"
	"image"
	"strings"
)

// Transmit uploads validated PNG bytes without placing pixels or moving the
// cursor. A caller can then place/crop one retained image in its own transaction.
func Transmit(png []byte, id uint32) string {
	if len(png) == 0 || id == 0 {
		return ""
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	for pos := 0; pos < len(encoded); pos += 4096 {
		end := min(len(encoded), pos+4096)
		more := 0
		if end < len(encoded) {
			more = 1
		}
		if pos == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=t,f=100,q=2,i=%d,m=%d;%s\x1b\\", id, more, encoded[pos:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, encoded[pos:end])
		}
	}
	return b.String()
}

// CropPlacement displays a source-pixel rectangle at zero-based cell coordinates.
// Cropping remains native on partially visible images, without stretching the
// entire source into a shorter cell rectangle or painting outside the viewport.
func CropPlacement(x, y, cols, rows int, id uint32, source image.Rectangle) string {
	if x < 0 || y < 0 || cols < 1 || rows < 1 || id == 0 || source.Min.X < 0 || source.Min.Y < 0 || source.Empty() {
		return ""
	}
	return fmt.Sprintf("\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=1,x=%d,y=%d,w=%d,h=%d,c=%d,r=%d,C=1,q=2\x1b\\\x1b8", y+1, x+1, id, source.Min.X, source.Min.Y, source.Dx(), source.Dy(), cols, rows)
}
