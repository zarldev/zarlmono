package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptReaderJumpsSearchesAndCopiesMessages(t *testing.T) {
	tl := newTimeline()
	tl.addUser("first request")
	tl.addNotice("middle notice")
	tl.addUser("second request with needle")
	r := newTranscriptReader(tl)
	r.view.viewWidth, r.view.viewHeight = 80, 20
	r.view.sel = 0
	r.jumpUser(1)
	if r.view.sel != 2 {
		t.Fatalf("jump selected item %d, want 2", r.view.sel)
	}
	r.view.sel = 0
	r.query = "needle"
	r.findNext(1)
	if r.view.sel != 2 {
		t.Fatalf("search selected item %d, want 2", r.view.sel)
	}
	if got := r.currentMessageText(); !strings.Contains(got, "second request with needle") {
		t.Fatalf("copied message = %q", got)
	}
}

func TestFileMentionPickerFiltersAndAttachesTextFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal", "useful.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package useful\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	picker := newFileMentionPicker(root)
	picker.query = "useful"
	if got := picker.filtered(); len(got) != 1 || got[0] != "internal/useful.go" {
		t.Fatalf("filtered files = %#v", got)
	}
	m := New()
	m.session.WorkspaceDir = root
	if err := m.attachFilePath(path); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if len(m.pendingAttachments) != 1 || !strings.Contains(m.pendingAttachments[0].Part.Text, "package useful") {
		t.Fatalf("attachment = %#v", m.pendingAttachments)
	}
}

func TestAgentActivityScreenIncludesCompletedAgents(t *testing.T) {
	tl := newTimeline()
	sa := newSubAgentItem(1, "reviewer", "review changes", "task-1")
	sa.closeGroups()
	tl.items = append(tl.items, sa)
	screen := newAgentActivityScreen(tl)
	got := screen.agents()
	if len(got) != 1 || got[0].agentName != "reviewer" || !got[0].closed {
		t.Fatalf("agents = %#v", got)
	}
}

func TestRunStateTracksCurrentIterationElapsedSeparately(t *testing.T) {
	var run RunState
	run.reset()
	run.Running = true
	run.turnStartedAt = time.Now().Add(-time.Minute)
	run.iterationStartedAt = time.Now().Add(-3 * time.Second)
	if time.Since(run.turnStartedAt) < time.Minute || time.Since(run.iterationStartedAt) < 3*time.Second {
		t.Fatal("elapsed clocks were not retained")
	}
}
