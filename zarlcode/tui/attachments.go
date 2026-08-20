package tui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/media"
)

const maxAttachedTextFileBytes = 512 * 1024

type pendingAttachment struct {
	Path string
	Name string
	Part llm.ContentPart
}

func (m *UI) attachFilePath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("attach file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("attach file: %s is not a regular file", filepath.Base(path))
	}
	if info.Size() > maxAttachedTextFileBytes {
		return fmt.Errorf("attach file: %s exceeds %s", filepath.Base(path), humanBytes(maxAttachedTextFileBytes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("attach file: %w", err)
	}
	rel, err := filepath.Rel(m.session.WorkspaceDir, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	text := fmt.Sprintf("Attached workspace file %q:\n\n%s", filepath.ToSlash(rel), data)
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		Path: path, Name: filepath.ToSlash(rel), Part: llm.TextPart(text),
	})
	return nil
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
		return fmt.Sprintf(" attached: %d files  ·  ", len(m.pendingAttachments))
	}
}
