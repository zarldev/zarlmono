package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

var exportSlugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

type sessionExportedMsg struct{ Path string }
type sessionExportFailedMsg struct{ Error string }

type sessionMarkdownExport struct {
	Title     string
	SessionID string
	Workspace string
	Provider  string
	Model     string
	AgentName string
	CreatedAt time.Time
	History   []llm.Message
}

func renderSessionMarkdown(export sessionMarkdownExport) string {
	var out strings.Builder
	title := strings.Join(strings.Fields(export.Title), " ")
	if title == "" {
		title = "Unnamed session"
	}
	fmt.Fprintf(&out, "# %s\n\n", title)
	fmt.Fprintf(&out, "- Session ID: %s\n", markdownCodeSpan(export.SessionID))
	if export.Workspace != "" {
		fmt.Fprintf(&out, "- Workspace: %s\n", markdownCodeSpan(export.Workspace))
	}
	if export.Provider != "" || export.Model != "" {
		model := strings.Trim(strings.TrimSpace(export.Provider+" / "+export.Model), "/ ")
		fmt.Fprintf(&out, "- Model: %s\n", markdownCodeSpan(model))
	}
	if export.AgentName != "" {
		fmt.Fprintf(&out, "- Agent: %s\n", markdownCodeSpan(export.AgentName))
	}
	if !export.CreatedAt.IsZero() {
		fmt.Fprintf(&out, "- Created: %s\n", export.CreatedAt.Format(time.RFC3339))
	}
	out.WriteString("\n## Conversation\n")
	for _, message := range export.History {
		body := exportMessageBody(message)
		if body == "" && strings.TrimSpace(message.ReasoningContent) == "" && len(message.ToolCalls) == 0 && message.ToolCallID == "" {
			continue
		}
		fmt.Fprintf(&out, "\n### %s\n\n", exportRoleTitle(message.Role))
		if message.ToolCallID != "" {
			fmt.Fprintf(&out, "- Tool call ID: %s\n\n", markdownCodeSpan(message.ToolCallID))
		}
		writeMarkdownBody(&out, body)
		if reasoning := strings.TrimSpace(message.ReasoningContent); reasoning != "" {
			out.WriteString("\n#### Reasoning\n\n")
			writeMarkdownBody(&out, reasoning)
		}
		for _, call := range message.ToolCalls {
			writeMarkdownToolCall(&out, call)
		}
	}
	return out.String()
}

func exportMessageBody(message llm.Message) string {
	blocks := make([]string, 0, 1+len(message.Parts))
	content := strings.TrimSpace(message.Content)
	if content != "" {
		blocks = append(blocks, content)
	}
	for _, part := range message.Parts {
		switch part.Type {
		case llm.ContentTypeText:
			text := strings.TrimSpace(part.Text)
			if text != "" && text != content {
				blocks = append(blocks, text)
			}
		default:
			if attachment := exportAttachmentPart(part); attachment != "" {
				blocks = append(blocks, attachment)
			}
		}
	}
	return strings.Join(blocks, "\n\n")
}

func exportAttachmentPart(part llm.ContentPart) string {
	switch part.Type {
	case llm.ContentTypeImage:
		if part.Image == nil {
			return "- Image attachment"
		}
		return exportMediaAttachment("Image", imagePartLabel(part.Image), part.Image.URL, part.Image.DataURI != "")
	case llm.ContentTypeAudio:
		if part.Audio == nil {
			return "- Audio attachment"
		}
		return exportMediaAttachment("Audio", audioPartLabel(part.Audio), "", part.Audio.DataURI != "")
	case llm.ContentTypeVideo:
		if part.Video == nil {
			return "- Video attachment"
		}
		return exportMediaAttachment("Video", videoPartLabel(part.Video), part.Video.URL, part.Video.DataURI != "")
	default:
		return "- Attachment"
	}
}

func exportMediaAttachment(kind, label, source string, embedded bool) string {
	line := "- " + kind + " attachment"
	if label != "" {
		line += ": " + markdownCodeSpan(label)
	}
	if source != "" {
		line += " — source " + markdownCodeSpan(source)
	} else if embedded {
		line += " — embedded data omitted"
	}
	return line
}

func writeMarkdownBody(out *strings.Builder, body string) {
	if body == "" {
		return
	}
	out.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		out.WriteByte('\n')
	}
}

func writeMarkdownToolCall(out *strings.Builder, call llm.ToolCall) {
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		name = "tool"
	}
	fmt.Fprintf(out, "\n#### Tool call: %s\n\n", name)
	if call.ID != "" {
		fmt.Fprintf(out, "- ID: %s\n", markdownCodeSpan(call.ID))
	}
	if arguments := strings.TrimSpace(call.Function.Arguments); arguments != "" {
		out.WriteString("\nArguments:\n\n")
		out.WriteString(markdownCodeBlock("json", arguments))
	}
}

func markdownCodeBlock(language, value string) string {
	longestRun, currentRun := 0, 0
	for _, r := range value {
		if r == '`' {
			currentRun++
			longestRun = max(longestRun, currentRun)
		} else {
			currentRun = 0
		}
	}
	fence := strings.Repeat("`", max(3, longestRun+1))
	return fence + language + "\n" + value + "\n" + fence + "\n"
}

func markdownCodeSpan(value string) string {
	longestRun, currentRun := 0, 0
	for _, r := range value {
		if r == '`' {
			currentRun++
			longestRun = max(longestRun, currentRun)
			continue
		}
		currentRun = 0
	}
	delimiter := strings.Repeat("`", longestRun+1)
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") || strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return delimiter + " " + value + " " + delimiter
	}
	return delimiter + value + delimiter
}

func exportRoleTitle(role string) string {
	switch strings.ToLower(role) {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Tool"
	case "system":
		return "System"
	default:
		if role == "" {
			return "Message"
		}
		runes := []rune(role)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
}

func safeExportSlug(label, id string) string {
	slug := slugPart(label)
	if slug == "" {
		slug = "session"
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	shortID := slugPart(id)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID != "" {
		slug += "-" + shortID
	}
	return slug + ".md"
}

func slugPart(value string) string {
	return strings.Trim(exportSlugInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-"), "-")
}

func sessionExportTarget(workspace, requested, label, id string) (string, string, bool) {
	if strings.TrimSpace(requested) == "" {
		return filepath.Join(workspace, ".zarlcode", "exports"), safeExportSlug(label, id), true
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	return filepath.Dir(path), filepath.Base(path), false
}

func (m *UI) exportSession(path string) tea.Cmd {
	if m.live == nil {
		return func() tea.Msg { return sessionExportFailedMsg{Error: "session is not active"} }
	}
	history := append([]llm.Message(nil), m.live.History()...)
	export := sessionMarkdownExport{
		Title:     m.session.Label,
		SessionID: m.session.ID,
		Workspace: m.session.WorkspaceDir,
		Provider:  m.session.Provider,
		Model:     m.session.Model,
		AgentName: m.session.LastAgentName,
		CreatedAt: m.session.CreatedAt,
		History:   history,
	}
	directory, filename, avoidCollision := sessionExportTarget(m.session.WorkspaceDir, strings.TrimSpace(path), m.session.Label, m.session.ID)
	return writeSessionExportCmd(directory, filename, renderSessionMarkdown(export), avoidCollision)
}

func writeSessionExportCmd(directory, filename, content string, avoidCollision bool) tea.Cmd {
	return func() tea.Msg {
		path, err := writeSessionExport(directory, filename, content, avoidCollision)
		if err != nil {
			return sessionExportFailedMsg{Error: err.Error()}
		}
		return sessionExportedMsg{Path: path}
	}
}

func writeSessionExport(directory, filename, content string, avoidCollision bool) (string, error) {
	if err := os.MkdirAll(directory, filesystem.ModePublicDir); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}

	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for suffix := 1; ; suffix++ {
		candidate := filepath.Join(directory, filename)
		if suffix > 1 {
			candidate = filepath.Join(directory, fmt.Sprintf("%s-%d%s", base, suffix, ext))
		}
		file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filesystem.ModePublicFile)
		if errors.Is(err, os.ErrExist) && avoidCollision {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create export: %w", err)
		}
		if _, err := file.WriteString(content); err != nil {
			_ = file.Close()
			_ = os.Remove(candidate)
			return "", fmt.Errorf("write export: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(candidate)
			return "", fmt.Errorf("close export: %w", err)
		}
		return candidate, nil
	}
}
