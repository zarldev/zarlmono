package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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

func TestAgentActivityScreenUsesOpenUtilityChrome(t *testing.T) {
	tl := newTimeline()
	screen := newAgentActivityScreen(tl)
	buf := uv.NewScreenBuffer(100, 24)
	screen.draw(buf, buf.Bounds())

	lines := strings.Split(ansi.Strip(buf.Render()), "\n")
	if !strings.Contains(lines[0], "agent activity") || !strings.Contains(lines[0], "0 delegated agents") {
		t.Fatalf("agent activity header missing context: %q", lines[0])
	}
	if strings.HasPrefix(lines[0], "┌") || strings.HasPrefix(lines[len(lines)-1], "└") {
		t.Fatalf("agent activity should not use an outer box:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[1], "─") || !strings.Contains(lines[2], "│") {
		t.Fatalf("agent activity missing utility divider or nav/detail split:\n%s", strings.Join(lines[:3], "\n"))
	}
	if !strings.Contains(lines[len(lines)-1], "esc close") {
		t.Fatalf("agent activity contextual footer missing close action: %q", lines[len(lines)-1])
	}
}

func TestAgentActivityScreenKeepsSelectedAgentAtSharedMinimum(t *testing.T) {
	tl := newTimeline()
	for i := 1; i <= 8; i++ {
		tl.items = append(tl.items, newSubAgentItem(1, fmt.Sprintf("agent-%d", i), "work", fmt.Sprintf("task-%d", i)))
	}
	screen := newAgentActivityScreen(tl)
	screen.cursor = 7
	buf := uv.NewScreenBuffer(42, 7)
	screen.draw(buf, buf.Bounds())
	out := ansi.Strip(buf.Render())
	lines := strings.Split(out, "\n")

	if !strings.Contains(out, "agent activity") || !strings.Contains(out, "agent-8") {
		t.Fatalf("narrow agent activity lost header or selected agent:\n%s", out)
	}
	if !strings.Contains(lines[len(lines)-1], "esc close") {
		t.Fatalf("narrow agent activity missing close action: %q", lines[len(lines)-1])
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
