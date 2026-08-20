package tui

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type fileViewerEntriesLoadedMsg struct {
	viewer     *fileViewer
	requestID  uint64
	dir        string
	selectName string
	entries    []os.DirEntry
	err        error
}

type fileViewerPreviewLoadedMsg struct {
	viewer    *fileViewer
	requestID uint64
	path      string
	content   string
	image     *fileViewerImagePreview
	directory fileViewerDirPreview
	err       error
}

func sortFileViewerEntries(entries []os.DirEntry) {
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
	})
}

func (m *UI) fileViewerInitialCmd(v *fileViewer) tea.Cmd {
	if v == nil {
		return nil
	}
	if v.initialPath != "" {
		return fileViewerEntriesCmd(v.requestResolvedPath(v.initialPath))
	}
	return fileViewerEntriesCmd(v.requestEntries(v.currentDir))
}

func fileViewerEntriesCmd(a actionFileViewerEntries) tea.Cmd {
	return func() tea.Msg {
		if a.ctx != nil && a.ctx.Err() != nil {
			err := a.ctx.Err()
			return fileViewerEntriesLoadedMsg{viewer: a.viewer, requestID: a.requestID, err: err}
		}
		dir, selected := a.dir, a.selectName
		if a.resolvePath {
			fullPath := a.path
			if !filepath.IsAbs(fullPath) {
				fullPath = filepath.Join(a.viewer.workspaceDir, fullPath)
			}
			fullPath = filepath.Clean(fullPath)
			rel, err := filepath.Rel(a.viewer.workspaceDir, fullPath)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fileViewerEntriesLoadedMsg{viewer: a.viewer, requestID: a.requestID, err: errors.New("path is outside workspace")}
			}
			if err := fileViewerPathWithinWorkspace(a.viewer.workspaceDir, fullPath); err != nil {
				return fileViewerEntriesLoadedMsg{viewer: a.viewer, requestID: a.requestID, err: err}
			}
			info, err := os.Stat(fullPath)
			if err != nil {
				return fileViewerEntriesLoadedMsg{viewer: a.viewer, requestID: a.requestID, err: err}
			}
			if info.IsDir() {
				dir = fullPath
			} else {
				dir, selected = filepath.Dir(fullPath), filepath.Base(fullPath)
			}
		}
		if err := fileViewerPathWithinWorkspace(a.viewer.workspaceDir, dir); err != nil {
			return fileViewerEntriesLoadedMsg{viewer: a.viewer, requestID: a.requestID, err: err}
		}
		entries, err := os.ReadDir(dir)
		if a.ctx != nil {
			if ctxErr := a.ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
		}
		if err == nil {
			sortFileViewerEntries(entries)
		}
		return fileViewerEntriesLoadedMsg{
			viewer: a.viewer, requestID: a.requestID, dir: dir,
			selectName: selected, entries: entries, err: err,
		}
	}
}

func fileViewerPreviewCmd(a actionFileViewerPreview) tea.Cmd {
	return func() tea.Msg {
		msg := fileViewerPreviewLoadedMsg{viewer: a.viewer, requestID: a.requestID, path: a.path}
		if a.ctx != nil && a.ctx.Err() != nil {
			err := a.ctx.Err()
			msg.err = err
			return msg
		}
		if err := fileViewerPathWithinWorkspace(a.viewer.workspaceDir, a.path); err != nil {
			msg.err = err
			return msg
		}
		if a.directory {
			msg.directory = loadFileViewerDirPreview(a.path)
			return msg
		}
		data, truncated, size, err := readFileViewerPreview(a.path)
		if a.ctx != nil {
			if ctxErr := a.ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
		}
		if err != nil {
			msg.err = err
			return msg
		}
		if preview, ok := loadFileViewerImagePreview(a.path, data, size); ok {
			msg.image = preview
			return msg
		}
		if fileViewerLooksBinary(data) {
			msg.content = fmt.Sprintf("binary file preview skipped (%s)", humanBytes(size))
			return msg
		}
		content, longLineTruncated := truncateFileViewerLongLines(string(data))
		content, lineTruncated := truncateFileViewerLines(content)
		if truncated {
			content += fmt.Sprintf("\n\n… preview truncated after %s (file is %s)", humanBytes(fileViewerMaxPreviewBytes), humanBytes(size))
		}
		if longLineTruncated {
			content += fmt.Sprintf("\n\n… long lines truncated after %d characters", fileViewerMaxLineRunes)
		}
		if lineTruncated {
			content += fmt.Sprintf("\n\n… preview truncated after %d lines", fileViewerMaxPreviewLines)
		}
		msg.content = content
		return msg
	}
}

func loadFileViewerDirPreview(path string) fileViewerDirPreview {
	preview := fileViewerDirPreview{path: path}
	f, err := os.Open(path)
	if err != nil {
		preview.err = err.Error()
		return preview
	}
	defer f.Close()
	entries, err := f.ReadDir(fileViewerDirPreviewLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		preview.err = err.Error()
		return preview
	}
	if len(entries) > fileViewerDirPreviewLimit {
		preview.truncated = true
		entries = entries[:fileViewerDirPreviewLimit]
	}
	sortFileViewerEntries(entries)
	preview.entries = make([]fileViewerPreviewEntry, 0, len(entries))
	for _, e := range entries {
		preview.entries = append(preview.entries, fileViewerPreviewEntry{name: e.Name(), isDir: e.IsDir()})
	}
	return preview
}

func (m *UI) handleFileViewerMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case fileViewerEntriesLoadedMsg:
		if !m.overlay.active() || m.overlay.top() != msg.viewer || msg.requestID != msg.viewer.entriesSeq {
			return nil, true
		}
		return m.applyFileViewerEntries(msg), true
	case fileViewerPreviewLoadedMsg:
		if !m.overlay.active() || m.overlay.top() != msg.viewer ||
			msg.requestID != msg.viewer.previewSeq || msg.path != msg.viewer.previewPath {
			return nil, true
		}
		msg.viewer.applyPreview(msg)
		return nil, true
	default:
		return nil, false
	}
}

func (m *UI) applyFileViewerEntries(msg fileViewerEntriesLoadedMsg) tea.Cmd {
	v := msg.viewer
	v.entriesLoading = false
	v.entriesErr = ""
	if msg.err != nil {
		v.entries = nil
		v.entriesErr = msg.err.Error()
		return nil
	}
	v.currentDir = msg.dir
	v.entries = msg.entries
	v.cursor = 0
	if msg.selectName != "" {
		for i, e := range v.entries {
			if e.Name() == msg.selectName {
				v.cursor = i
				break
			}
		}
	}
	if v.cursor >= len(v.entries) {
		v.cursor = max(0, len(v.entries)-1)
	}
	if a, ok := v.requestSelectedPreview(); ok {
		return fileViewerPreviewCmd(a)
	}
	return nil
}

func (v *fileViewer) applyPreview(msg fileViewerPreviewLoadedMsg) {
	v.previewLoading = false
	v.viewingFile = msg.path
	v.imagePreview = nil
	v.dirPreview = fileViewerDirPreview{}
	v.fileContent = ""
	if msg.err != nil {
		v.fileContent = "could not read: " + msg.err.Error()
		return
	}
	if msg.directory.path != "" {
		v.viewingFile = ""
		v.dirPreview = msg.directory
		return
	}
	v.imagePreview = msg.image
	v.fileContent = msg.content
}
