package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/media"
)

const maxAttachedTextFileBytes = 512 * 1024
const maxTranscriptAttachmentNameBytes = 512

type attachmentMetadata struct {
	Name     string
	MIMEType string
	Size     int64
}

type pendingAttachment struct {
	Part     llm.ContentPart
	Metadata attachmentMetadata
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
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = filepath.Base(path)
	}
	text := fmt.Sprintf("Attached workspace file %q:\n\n%s", filepath.ToSlash(rel), data)
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		Part:     llm.TextPart(text),
		Metadata: attachmentMetadata{Name: transcriptAttachmentName(filepath.ToSlash(rel)), Size: info.Size()},
	})
	return nil
}

func (m *UI) attachImagePath(path string) error {
	part, err := media.ImagePartFromFile(path)
	if err != nil {
		return err
	}
	m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
		Part: part,
		Metadata: attachmentMetadata{
			Name: transcriptAttachmentName(filepath.Base(path)), MIMEType: imageMIMEType(part), Size: fileSize(path),
		},
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

func attachmentMetadataOf(attachments []pendingAttachment) []attachmentMetadata {
	metadata := make([]attachmentMetadata, len(attachments))
	for i, attachment := range attachments {
		metadata[i] = attachment.Metadata
	}
	return metadata
}

func imageMIMEType(part llm.ContentPart) string {
	if part.Image == nil {
		return ""
	}
	return part.Image.MIMEType
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func transcriptAttachmentName(name string) string {
	if len(name) <= maxTranscriptAttachmentNameBytes {
		return name
	}
	name = name[:maxTranscriptAttachmentNameBytes]
	for !utf8.ValidString(name) {
		name = name[:len(name)-1]
	}
	return name
}
