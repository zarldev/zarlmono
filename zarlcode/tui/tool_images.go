package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	toolPreviewMaxImages = 4
	toolPreviewMaxBytes  = 4 * 1024 * 1024
	toolPreviewMaxPixels = 4_000_000
)

// toolResultPresentation belongs only to the timeline. It borrows the original
// result and owns small decoded thumbnails, not a second copy of attachments.
// Preparing once on completion keeps decoding out of render/layout passes.
type toolResultPresentation struct {
	data   any
	images []toolImagePreview
}

type toolImagePreview struct {
	image image.Image
	label string
	png   []byte // owned, bounded PNG for native terminal graphics; nil uses cells
}

func prepareToolResultPresentation(data any, parts []llm.ContentPart, native bool) any {
	var images []toolImagePreview
	for _, part := range parts {
		if part.Type != llm.ContentTypeImage || part.Image == nil {
			continue
		}
		if len(images) == toolPreviewMaxImages {
			images = append(images, toolImagePreview{label: "Additional images omitted from preview"})
			break
		}
		images = append(images, decodeToolImagePreview(*part.Image, native))
	}
	if len(images) == 0 {
		return data
	}
	return toolResultPresentation{data: data, images: images}
}

// Decode only embedded images. Rendering must never fetch a URL or open a path
// supplied by a tool. Check compressed bytes and dimensions before pixel decode.
func decodeToolImagePreview(source llm.ImageData, native bool) toolImagePreview {
	unavailable := toolImagePreview{label: "Image preview unavailable"}
	if source.DataURI == "" {
		unavailable.label += " · no embedded image"
		return unavailable
	}
	if len(source.DataURI) > toolPreviewMaxBytes {
		unavailable.label += " · encoded image exceeds 4 MiB"
		return unavailable
	}
	header, payload, ok := strings.Cut(source.DataURI, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64" && header != "data:image/gif;base64") {
		unavailable.label += " · unsupported image format"
		return unavailable
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return unavailable
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return unavailable
	}
	if config.Width > toolPreviewMaxPixels/config.Height {
		unavailable.label += " · image exceeds 4 million pixels"
		return unavailable
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return unavailable
	}
	// Retain only a small, aspect-preserving thumbnail. The original remains in
	// the tool result/history and is not rewritten by this presentation path.
	maxW, maxH := 160.0, 64.0
	if native {
		maxW, maxH = 1280, 960
	}
	scale := min(1.0, maxW/float64(config.Width), maxH/float64(config.Height))
	w, h := max(1, int(float64(config.Width)*scale)), max(1, int(float64(config.Height)*scale))
	thumb := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			thumb.Set(x, y, sampleImageColor(img, img.Bounds(), x, y, w, h))
		}
	}
	preview := toolImagePreview{image: thumb, label: fmt.Sprintf("Image · %d × %d", config.Width, config.Height)}
	if native {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, thumb); err == nil {
			preview.png = encoded.Bytes()
		}
	}
	return preview
}

func renderToolImagePreviews(width int, images []toolImagePreview) []string {
	lines, _ := layoutToolImagePreviews(width, images)
	return lines
}

// layoutToolImagePreviews owns both the reserved rows and their native image
// coordinates. Labels, wrapping, and row budgets cannot drift between the two.
func layoutToolImagePreviews(width int, images []toolImagePreview) ([]string, []toolImagePlacement) {
	var lines []string
	var placements []toolImagePlacement
	for _, preview := range images {
		lines = append(lines, webResultLines(width, preview.label, palette.Muted.On)...)
		maxCols, maxRows := min(80, width), max(1, 16/len(images))
		if len(preview.png) == 0 {
			lines = append(lines, renderFileViewerImage(preview.image, maxCols, maxRows)...)
			continue
		}
		maxCols, maxRows = min(96, width), max(1, 20/len(images))
		bounds := preview.image.Bounds()
		cols, rows := fileViewerImageCells(bounds.Dx(), bounds.Dy(), maxCols, maxRows)
		placements = append(placements, toolImagePlacement{y: len(lines), cols: cols, rows: rows, pixelWidth: bounds.Dx(), pixelHeight: bounds.Dy(), png: preview.png})
		for range rows {
			lines = append(lines, "")
		}
	}
	return lines, placements
}
