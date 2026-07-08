package tui

import (
	"fmt"
	"path/filepath"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/media"
)

type pendingAttachment struct {
	Path string
	Name string
	Part llm.ContentPart
}

func (m *UI) attachImagePath(path string) error {
	part, err := media.ImagePartFromFile(path)
	if err != nil {
		return err
	}
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		Path: path,
		Name: filepath.Base(path),
		Part: part,
	})
	return nil
}

func (m *UI) attachmentParts() []llm.ContentPart {
	if len(m.pendingAttachments) == 0 {
		return nil
	}
	parts := make([]llm.ContentPart, 0, len(m.pendingAttachments))
	for _, a := range m.pendingAttachments {
		parts = append(parts, a.Part)
	}
	return parts
}

func (m *UI) attachmentSummary() string {
	switch len(m.pendingAttachments) {
	case 0:
		return ""
	case 1:
		return " attached: " + m.pendingAttachments[0].Name + "  ·  "
	default:
		return fmt.Sprintf(" attached: %d images  ·  ", len(m.pendingAttachments))
	}
}
