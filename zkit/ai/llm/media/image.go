package media

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const maxSniffBytes = 512

// ImagePartFromFile reads path and returns an image content part backed by a
// base64 data URI. The MIME type is inferred from the extension first, then
// from the file content.
func ImagePartFromFile(path string) (llm.ContentPart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return llm.ContentPart{}, fmt.Errorf("read image %q: %w", path, err)
	}
	mime := ImageMIME(path, data)
	if !strings.HasPrefix(mime, "image/") {
		return llm.ContentPart{}, fmt.Errorf("%q is not a supported image MIME type (%s)", path, mime)
	}
	return ImagePartFromBytes(data, mime), nil
}

// ImagePartFromBytes returns an image content part backed by a base64 data URI.
func ImagePartFromBytes(data []byte, mime string) llm.ContentPart {
	if mime == "" {
		mime = http.DetectContentType(data[:min(len(data), maxSniffBytes)])
	}
	dataURI := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	return llm.ImagePartFromDataURI(dataURI, mime)
}

// ImageMIME infers an image MIME type from path and data.
func ImageMIME(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return http.DetectContentType(data[:min(len(data), maxSniffBytes)])
}

// IsImagePath reports whether path has an extension commonly used for images.
func IsImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}
