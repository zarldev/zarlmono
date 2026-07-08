package media_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/media"
)

func TestImagePartFromBytes(t *testing.T) {
	part := media.ImagePartFromBytes([]byte("png-ish"), "image/png")
	if part.Image == nil {
		t.Fatal("image part missing image data")
	}
	wantPrefix := "data:image/png;base64,"
	if !strings.HasPrefix(part.Image.DataURI, wantPrefix) {
		t.Fatalf("data URI = %q, want prefix %q", part.Image.DataURI, wantPrefix)
	}
	encoded := strings.TrimPrefix(part.Image.DataURI, wantPrefix)
	got, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode data URI: %v", err)
	}
	if string(got) != "png-ish" {
		t.Fatalf("payload = %q, want png-ish", got)
	}
}

func TestIsImagePath(t *testing.T) {
	for _, path := range []string{"a.png", "b.JPG", "c.webp", "d.gif"} {
		if !media.IsImagePath(path) {
			t.Errorf("IsImagePath(%q) = false", path)
		}
	}
	if media.IsImagePath("notes.txt") {
		t.Error("txt should not be treated as image")
	}
}
