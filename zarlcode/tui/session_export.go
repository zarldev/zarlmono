package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
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
	export := sessionMarkdownExport{
		Title:     m.session.Label,
		SessionID: m.session.ID,
		Workspace: m.session.WorkspaceDir,
		Provider:  m.session.Provider,
		Model:     m.session.Model,
		AgentName: m.session.LastAgentName,
		CreatedAt: m.session.CreatedAt,
	}
	directory, filename, avoidCollision := sessionExportTarget(m.session.WorkspaceDir, strings.TrimSpace(path), m.session.Label, m.session.ID)
	content := transcript.Markdown(transcript.MarkdownMetadata{
		Title: export.Title, SessionID: export.SessionID, Workspace: export.Workspace,
		Provider: export.Provider, Model: export.Model, AgentName: export.AgentName, CreatedAt: export.CreatedAt,
	}, m.timeline.transcriptThread())
	return writeSessionExportCmd(directory, filename, content, avoidCollision)
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
