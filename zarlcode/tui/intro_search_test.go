package tui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSavedSessionSearchAcceptsLetterP(t *testing.T) {
	ctx := t.Context()
	workspace := t.TempDir()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveActiveSession(ctx, db.SessionRecord{
		ID:        "session-1",
		Workspace: workspace,
		Label:     "parser cleanup",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
	ui.ActivateIntro(ctx)
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model, _ = model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})

	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "search sessions: p") {
		t.Fatalf("search did not accept letter p:\n%s", out)
	}
	if strings.Contains(out, "★ parser cleanup") {
		t.Fatalf("typing p in search unexpectedly pinned the session:\n%s", out)
	}
}
