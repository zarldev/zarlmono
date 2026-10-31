package termimage_test

import (
	"image"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/termimage"
)

func TestOwnedImages(t *testing.T) {
	data := []byte("pixels")
	id := termimage.ID(data)
	if id == 0 || id&0x80000000 != 0 || id != termimage.ID(data) {
		t.Fatal("unstable ID")
	}
	frame := termimage.Frame(2, 3, 10, 5, data, id)
	if !strings.HasPrefix(frame, "\x1b7\x1b[4;3H") || !strings.HasSuffix(frame, "\x1b8") {
		t.Fatalf("cursor: %q", frame)
	}
	if strings.Contains(termimage.Delete(id), "d=A") || !strings.Contains(termimage.Delete(id), "d=I,i=") {
		t.Fatal("unowned delete")
	}
	if !strings.Contains(termimage.Placement(0, 0, 10, 5, id, -1), "z=-1") {
		t.Fatal("depth")
	}
}
func TestChunkingAndBounds(t *testing.T) {
	frame := termimage.Frame(0, 0, 10, 5, make([]byte, 10000), 1)
	if strings.Count(frame, "\x1b_G") != 4 || !strings.Contains(frame, "m=0;") {
		t.Fatal("chunking")
	}
	if termimage.Frame(-1, 0, 1, 1, []byte{1}, 1) != "" || termimage.Placement(0, 0, 0, 1, 1, 0) != "" {
		t.Fatal("bounds")
	}
	if termimage.Delete(0) != "" {
		t.Fatal("zero ID")
	}
}
func TestFallback(t *testing.T) {
	rows := termimage.Fallback(image.NewRGBA(image.Rect(0, 0, 12, 6)), 10, 5)
	if len(rows) != 5 {
		t.Fatal(len(rows))
	}
	for _, row := range rows {
		if strings.Count(row, "▀") != 10 || !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatal("row")
		}
	}
	c, r := termimage.Cells(400, 200, 20, 20)
	if c != 20 || r != 5 {
		t.Fatalf("%d,%d", c, r)
	}
	c, r = termimage.Cells(0, 2, 20, 20)
	if c != 0 || r != 0 {
		t.Fatal("invalid dimensions")
	}
}
