package termimage_test

import (
	"image"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/termimage"
)

func TestTransmitDoesNotPlaceOrMove(t *testing.T) {
	stream := termimage.Transmit(make([]byte, 10000), 12)
	if !strings.HasPrefix(stream, "\x1b_Ga=t,f=100,q=2,i=12,") || strings.Contains(stream, "a=T") || strings.Contains(stream, "\x1b[") {
		t.Fatal("upload moved/placed pixels")
	}
	if strings.Count(stream, "\x1b_G") != 4 || !strings.Contains(stream, "m=0;") {
		t.Fatal("chunking")
	}
	if termimage.Transmit(nil, 12) != "" {
		t.Fatal("empty upload")
	}
}
func TestCropUsesSourcePixelsAndPreservesCursor(t *testing.T) {
	placement := termimage.CropPlacement(8, 12, 28, 3, 12, image.Rect(0, 200, 640, 400))
	for _, want := range []string{"\x1b7\x1b[13;9H", "a=p,i=12,p=1,x=0,y=200,w=640,h=200,c=28,r=3,C=1", "\x1b8"} {
		if !strings.Contains(placement, want) {
			t.Fatalf("missing %q in %q", want, placement)
		}
	}
	if termimage.CropPlacement(0, 0, 4, 2, 1, image.Rect(-1, 0, 20, 20)) != "" {
		t.Fatal("invalid crop")
	}
}
