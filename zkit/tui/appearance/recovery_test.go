package appearance_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestUnknownOverrideFallsThroughAndPickerCanRepair(t *testing.T) {
	s, path := store(t)
	ctx := t.Context()
	if err := s.Set(ctx, appearance.Global(), "retro-amber"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO overrides(scope,app,workspace,name) VALUES('workspace','chat','project','removed-theme')`)
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatalf("seed unknown override: %v / %v", err, closeErr)
	}
	resolved, err := s.Resolve(ctx, "chat", "project")
	if err == nil || !strings.Contains(err.Error(), "removed-theme") || resolved.Name != "retro-amber" || resolved.Scope != "global" {
		t.Fatalf("valid inherited theme lost: %+v / %v", resolved, err)
	}
	picker, err := appearance.NewPicker(ctx, s, "chat", "project", resolved.Theme)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(picker.Lines(100, 15), "\n"), "notice:") {
		t.Fatal("repair picker concealed persisted-theme diagnostic")
	}
	resolved, err = picker.Inherit(ctx)
	if err != nil || resolved.Name != "retro-amber" {
		t.Fatalf("inherit did not repair invalid override: %+v / %v", resolved, err)
	}
	if strings.Contains(strings.Join(picker.Lines(100, 15), "\n"), "notice:") {
		t.Fatal("successful repair retained stale diagnostic")
	}
}

func TestInterruptedEmptyStoreDoesNotPoisonResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "appearance.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s := appearance.New(path)
	resolved, err := s.Resolve(t.Context(), "app", "workspace")
	if err != nil || resolved.Scope != "default" {
		t.Fatalf("empty first creation should inherit: %+v / %v", resolved, err)
	}
	if _, exists, err := s.Lookup(t.Context(), appearance.Global()); err != nil || exists {
		t.Fatalf("empty store contains a selection: %v / %v", exists, err)
	}
	if err := s.Reset(t.Context(), appearance.Global()); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(t.Context(), appearance.Global(), "terminal-green"); err != nil {
		t.Fatal(err)
	}
	choice(t, s, "app", "workspace", "terminal-green", "global")
}

func TestPickerRetainsExactOriginAndStyles(t *testing.T) {
	s, _ := store(t)
	if err := s.Set(t.Context(), appearance.Global(), "retro-amber"); err != nil {
		t.Fatal(err)
	}
	original := theme.Theme{Name: "unregistered-live-palette", Primary: "#abcdef", Selection: "#112233"}
	picker, err := appearance.NewPicker(t.Context(), s, "app", "workspace", original)
	if err != nil {
		t.Fatal(err)
	}
	if picker.Original() != original || picker.Name() != "retro-amber" {
		t.Fatal("unknown live palette replaced origin or selected arbitrary catalogue entry")
	}
	picker.Move(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	choice, err := picker.Commit(ctx)
	if err == nil || choice.Name != "retro-amber" || picker.Original() != original {
		t.Fatalf("failed save changed opening/effective state: %+v / %v", choice, err)
	}
	palette := picker.Preview()
	styled := strings.Join(picker.LinesWithStyle(100, 12, chrome.Style{Theme: palette}), "\n")
	if !strings.Contains(styled, palette.Selection.BG()) {
		t.Fatal("picker selection is not color-coded")
	}
	for _, line := range picker.LinesWithStyle(100, 12, chrome.Style{Theme: palette, Plain: true, ASCII: true}) {
		if strings.Contains(line, "\x1b") || ansi.StringWidth(line) != 100 || strings.Contains(line, "·") {
			t.Fatalf("plain/ASCII picker output: %q", line)
		}
	}
}

func TestCanonicalWorkspaceSharesSymlinkIdentity(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "project")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "shortcut")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	a, err := appearance.CanonicalWorkspace(target)
	if err != nil {
		t.Fatal(err)
	}
	b, err := appearance.CanonicalWorkspace(alias)
	if err != nil || a != b {
		t.Fatalf("workspace aliases diverge: %q / %q / %v", a, b, err)
	}
}
