package tui

import (
	"fmt"
	"image"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// toolImagePlacement borrows its PNG from the result presentation. Coordinates
// are item-local cells until mapped into the visible timeline viewport.
type toolImagePlacement struct {
	x, y, cols, rows        int
	pixelWidth, pixelHeight int
	png                     []byte
}

// itemToolImages follows the same cached child offsets and gutters as text and
// disclosure hit-testing. Collapsed containers never expose descendant images.
func itemToolImages(it item, width int) []toolImagePlacement {
	var out []toolImagePlacement
	var children []item
	var block childBlock
	var childWidth, dx, dy int
	switch v := it.(type) {
	case *toolItem:
		if !v.expanded {
			return nil
		}
		dx = v.depth * 2
		if result, ok := v.data.(toolResultPresentation); ok && !v.suppressesResultBody() {
			_, images := layoutToolImagePreviews(max(1, width-dx-4), result.images)
			for _, p := range images {
				// The body renderer retains at most toolResultMaxLines rows,
				// followed by a textual truncation notice.
				if p.y+p.rows <= toolResultMaxLines {
					p.x += dx + 4
					p.y++ // tool header
					out = append(out, p)
				}
			}
		}
		children, childWidth = v.childItems(), width
		block = v.layout.render(children, childWidth, v.version())
		dy = len(v.resultBodyLines(width))
	case *groupItem:
		if !v.expanded {
			return nil
		}
		children, childWidth = v.children, v.childWidth(width)
		block = v.layout.render(children, childWidth, v.version())
		dx = v.depth*2 + len(childIndent)
		if v.nested {
			dx += 2
		}
	case *subAgentItem:
		if !v.expanded {
			return nil
		}
		children, childWidth = v.children, v.childWidth(width)
		block = v.layout.render(children, childWidth, v.version())
		dx = v.depth*2 + len(childIndent)
		if v.nested {
			dx += 2
		}
	}
	for i, child := range children {
		for _, p := range itemToolImages(child, childWidth) {
			p.x += dx
			p.y += dy + block.offsets[i]
			out = append(out, p)
		}
	}
	return out
}

// timelineGraphics maps only visible items into terminal coordinates. Cropping
// uses source pixels, so scrolling reveals a slice rather than shrinking the
// whole screenshot to fit the remaining viewport rows.
func (tl *timeline) timelineGraphics(viewport uv.Rectangle, visibleRows int) string {
	var out strings.Builder
	for first := 0; first < visibleRows; {
		idx := tl.visItem[first]
		end := first + 1
		for end < visibleRows && tl.visItem[end] == idx {
			end++
		}
		if idx >= 0 {
			originY := viewport.Min.Y + first - tl.visLocal[first]
			clip := image.Rect(viewport.Min.X, viewport.Min.Y+first, viewport.Max.X, viewport.Min.Y+end)
			for _, p := range itemToolImages(tl.items[idx], viewport.Dx()) {
				p.x += viewport.Min.X
				p.y += originY
				out.WriteString(p.kittyGraphics(clip))
			}
		}
		first = end
	}
	return out.String()
}

func (p toolImagePlacement) kittyGraphics(clip image.Rectangle) string {
	full := image.Rect(p.x, p.y, p.x+p.cols, p.y+p.rows)
	visible := full.Intersect(clip)
	if visible.Empty() {
		return ""
	}
	x0 := (visible.Min.X - full.Min.X) * p.pixelWidth / p.cols
	y0 := (visible.Min.Y - full.Min.Y) * p.pixelHeight / p.rows
	x1 := (visible.Max.X - full.Min.X) * p.pixelWidth / p.cols
	y1 := (visible.Max.Y - full.Min.Y) * p.pixelHeight / p.rows
	crop := fmt.Sprintf(",x=%d,y=%d,w=%d,h=%d", x0, y0, max(1, x1-x0), max(1, y1-y0))
	return kittyGraphicsAt(visible.Min.X+1, visible.Min.Y+1, visible.Dx(), visible.Dy(), p.png, crop)
}
