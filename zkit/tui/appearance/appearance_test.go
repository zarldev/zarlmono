package appearance_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	_ "modernc.org/sqlite"

	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func store(t *testing.T) (*appearance.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config space", "appearance.db")
	return appearance.New(path), path
}
func names(t *testing.T) []string {
	t.Helper()
	b := theme.Builtins()
	if len(b) < 3 {
		t.Fatal("need three palettes")
	}
	return []string{b[0].Name, b[1].Name, b[2].Name}
}
func choice(t *testing.T, s *appearance.Store, app, workspace, name, scope string) {
	t.Helper()
	c, err := s.Resolve(t.Context(), app, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != name || c.Scope != scope || c.Theme.Name != name {
		t.Fatalf("choice = %+v, want %s %s", c, name, scope)
	}
}
func TestPrecedenceAndReset(t *testing.T) {
	s, _ := store(t)
	ctx := t.Context()
	n := names(t)
	choice(t, s, "app", "workspace", appearance.DefaultTheme, "default")
	for _, tc := range []struct {
		target appearance.Target
		name   string
	}{{appearance.Global(), n[0]}, {appearance.App("app"), n[1]}, {appearance.Workspace("app", "workspace"), n[2]}} {
		if err := s.Set(ctx, tc.target, tc.name); err != nil {
			t.Fatal(err)
		}
	}
	choice(t, s, "other", "space", n[0], "global")
	choice(t, s, "app", "other", n[1], "app")
	choice(t, s, "app", "workspace", n[2], "workspace")
	for _, tc := range []struct {
		target      appearance.Target
		name, scope string
	}{{appearance.Workspace("app", "workspace"), n[1], "app"}, {appearance.App("app"), n[0], "global"}, {appearance.Global(), appearance.DefaultTheme, "default"}} {
		if err := s.Reset(ctx, tc.target); err != nil {
			t.Fatal(err)
		}
		choice(t, s, "app", "workspace", tc.name, tc.scope)
	}
	if name, set, err := s.Lookup(ctx, appearance.App("app")); err != nil || set || name != "" {
		t.Fatalf("lookup = %q %v %v", name, set, err)
	}
}
func TestMissingReadResetNoFiles(t *testing.T) {
	s, path := store(t)
	ctx := t.Context()
	if _, err := s.Resolve(ctx, "a", "w"); err != nil {
		t.Fatal(err)
	}
	if _, set, err := s.Lookup(ctx, appearance.Global()); err != nil || set {
		t.Fatalf("lookup: %v %v", set, err)
	}
	if err := s.Reset(ctx, appearance.Global()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("read created directory: %v", err)
	}
	if err := s.Set(ctx, appearance.App("a"), appearance.DefaultTheme); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Dir(path), path} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0077 != 0 {
			t.Fatalf("insecure permissions %s: %v", p, st.Mode())
		}
	}
}
func TestValidationAndCorruption(t *testing.T) {
	s, path := store(t)
	ctx := t.Context()
	for _, scope := range []string{"", "unknown", "workspace", "app"} {
		if _, err := appearance.TargetFor(scope, "", ""); err == nil {
			t.Fatalf("accepted %q", scope)
		}
	}
	if _, err := appearance.TargetFor("workspace", "a", "w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, appearance.Workspace("a", ""), appearance.DefaultTheme); err == nil {
		t.Fatal("accepted empty workspace")
	}
	if err := s.Set(ctx, appearance.Global(), "not-a-theme"); err == nil {
		t.Fatal("accepted unknown theme")
	}
	if _, err := s.Resolve(ctx, "", "w"); err == nil {
		t.Fatal("accepted orphan workspace")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE overrides(scope TEXT, app TEXT, workspace TEXT, name TEXT); INSERT INTO overrides VALUES('global','','','unknown-palette')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := s.Resolve(ctx, "a", "w")
	if err == nil || !strings.Contains(err.Error(), "unknown-palette") || c.Name != appearance.DefaultTheme || c.Scope != "default" {
		t.Fatalf("unknown = %+v %v", c, err)
	}
	name, set, err := s.Lookup(ctx, appearance.Global())
	if err != nil || !set || name != "unknown-palette" {
		t.Fatalf("lookup = %q %v %v", name, set, err)
	}
	if err := os.WriteFile(path, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = s.Resolve(ctx, "a", "w")
	if err == nil || c.Name != appearance.DefaultTheme {
		t.Fatalf("corrupt = %+v %v", c, err)
	}
}
func TestConcurrentWriters(t *testing.T) {
	s, _ := store(t)
	ctx := t.Context()
	n := names(t)
	// Pre-create schema so these workers exercise concurrent independent handles.
	if err := s.Set(ctx, appearance.Global(), n[0]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app := "app" + string(rune('A'+i))
			errs <- s.Set(ctx, appearance.App(app), n[i%3])
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app := "app" + string(rune('A'+i))
			errs <- s.Set(ctx, appearance.Workspace(app, "shared workspace"), n[(i+1)%3])
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		app := "app" + string(rune('A'+i))
		choice(t, s, app, "other", n[i%3], "app")
		choice(t, s, app, "shared workspace", n[(i+1)%3], "workspace")
	}
}
func TestPickerPreviewAndGeometry(t *testing.T) {
	s, path := store(t)
	ctx := t.Context()
	n := names(t)
	original, _ := theme.ByName(n[0])
	p, err := appearance.NewPicker(ctx, s, "app", "my workspace", original)
	if err != nil {
		t.Fatal(err)
	}
	if p.Scope() != "workspace" || p.Name() != original.Name {
		t.Fatalf("initial picker %s %s", p.Scope(), p.Name())
	}
	p.Move(1)
	if p.Preview().Name == original.Name {
		t.Fatal("no preview change")
	}
	p.CycleScope()
	if p.Scope() != "global" {
		t.Fatalf("scope = %s", p.Scope())
	}
	for h := range 12 {
		for w := range 16 {
			lines := p.Lines(w, h)
			if len(lines) > h {
				t.Fatal("height overflow")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) != w {
					t.Fatalf("width %d: %q", w, line)
				}
			}
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview wrote config: %v", err)
	}
	p.CycleScope()
	p.CycleScope()
	if p.Scope() != "workspace" {
		t.Fatal(p.Scope())
	}
	selected := p.Name()
	c, err := p.Commit(ctx)
	if err != nil || c.Name != selected || c.Scope != "workspace" {
		t.Fatalf("commit %+v %v", c, err)
	}
	c, err = p.Inherit(ctx)
	if err != nil || c.Scope != "default" {
		t.Fatalf("inherit %+v %v", c, err)
	}
}
