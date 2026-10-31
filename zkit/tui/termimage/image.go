// Package termimage encodes owned Kitty images and portable half-block previews.
// It performs no terminal I/O and does not retain images or own goroutines.
package termimage

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"strings"
)

// ID returns a stable nonzero ID in the lower half of Kitty's image namespace.
func ID(data []byte) uint32 {
	sum := sha256.Sum256(data)
	id := binary.BigEndian.Uint32(sum[:4]) & 0x7fffffff
	if id == 0 {
		return 1
	}
	return id
}

// Frame transmits PNG bytes and places them at zero-based cell coordinates.
// It preserves the cursor and chunks the encoded payload at 4096 bytes.
func Frame(x, y, cols, rows int, png []byte, id uint32) string {
	if x < 0 || y < 0 || cols < 1 || rows < 1 || len(png) == 0 || id == 0 {
		return ""
	}
	encoded := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b7\x1b[%d;%dH", y+1, x+1)
	for pos := 0; pos < len(encoded); pos += 4096 {
		end := min(len(encoded), pos+4096)
		more := 0
		if end < len(encoded) {
			more = 1
		}
		if pos == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,C=1,i=%d,p=1,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, encoded[pos:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, encoded[pos:end])
		}
	}
	b.WriteString("\x1b8")
	return b.String()
}

// Placement places existing pixels without retransmission. Depth zero uses the
// terminal default; negative depth places pixels behind text.
func Placement(x, y, cols, rows int, id uint32, depth int) string {
	if x < 0 || y < 0 || cols < 1 || rows < 1 || id == 0 {
		return ""
	}
	z := ""
	if depth != 0 {
		z = fmt.Sprintf(",z=%d", depth)
	}
	return fmt.Sprintf("\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=1,c=%d,r=%d%s,q=2\x1b\\\x1b8", y+1, x+1, id, cols, rows, z)
}

// Delete removes only the owned image and its placements, never other images.
func Delete(id uint32) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}

// Cells fits pixels inside a cell rectangle, assuming cells twice as tall as wide.
func Cells(width, height, maxCols, maxRows int) (int, int) {
	if width <= 0 || height <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	scale := min(float64(maxCols)/float64(width), float64(maxRows*2)/float64(height))
	return max(1, min(maxCols, int(float64(width)*scale))), max(1, min(maxRows, int(float64(height)*scale/2)))
}

// Fallback samples a letterboxed image into truecolor half-block rows. The caller
// controls output dimensions; empty images and dimensions produce no rows.
func Fallback(img image.Image, cols, rows int) []string {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || cols <= 0 || rows <= 0 {
		return nil
	}
	out := make([]string, rows)
	scale := min(float64(cols)/float64(w), float64(rows*2)/float64(h))
	dw, dh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	ox, oy := (cols-dw)/2, (rows*2-dh)/2
	pixel := func(x, y int) color.RGBA {
		if x < ox || x >= ox+dw || y < oy || y >= oy+dh {
			return color.RGBA{30, 30, 30, 255}
		}
		r, g, b, _ := img.At(bounds.Min.X+(x-ox)*w/dw, bounds.Min.Y+(y-oy)*h/dh).RGBA()
		return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
	}
	for y := 0; y < rows*2; y += 2 {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			a, c := pixel(x, y), pixel(x, y+1)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", a.R, a.G, a.B, c.R, c.G, c.B)
		}
		b.WriteString("\x1b[0m")
		out[y/2] = b.String()
	}
	return out
}
