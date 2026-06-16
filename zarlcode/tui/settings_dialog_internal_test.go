package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// The settings surface is a full-screen takeover using the shared pane frame,
// with a single in-pane footer. Smoke-test that draw renders the chrome and
// drops the old centered-box footer hint.
func TestSettingsDialog_DrawFullScreen(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)

	buf := uv.NewScreenBuffer(120, 30)
	d.draw(buf, buf.Bounds())
	out := buf.Render()

	for _, want := range []string{"settings", "esc done", "providers", "appearance", "provider", "category"} {
		if !strings.Contains(out, want) {
			t.Errorf("full-screen draw missing %q", want)
		}
	}
	if strings.Contains(out, "tab pane") {
		t.Error("old centered-box footer hint 'tab pane' should be gone")
	}

	// A too-small area must bail without panicking.
	d.draw(uv.NewScreenBuffer(10, 4), uv.Rect(0, 0, 10, 4))
}

func uvScreen(w, h int) uv.ScreenBuffer { return uv.NewScreenBuffer(w, h) }
func uvRect(w, h int) uv.Rectangle      { return uv.Rect(0, 0, w, h) }

func skey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }
func tkey(s string) tea.KeyPressMsg  { return tea.KeyPressMsg{Text: s, Code: []rune(s)[0]} }
func typeStr(d *settingsDialog, s string) {
	for _, ch := range s {
		d.handleKey(tea.KeyPressMsg{Text: string(ch), Code: ch})
	}
}

func TestSettingsDialog_TextEditPersistsAndPromotes(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)

	d.handleKey(skey(tea.KeyTab)) // nav → rows
	if !d.focusRows {
		t.Fatal("tab should focus the detail rows")
	}
	d.handleKey(skey(tea.KeyDown)) // provider → model
	if d.curRow().key != prefs.KeyModel {
		t.Fatalf("expected model row, got %q", d.curRow().key)
	}
	d.handleKey(skey(tea.KeyEnter)) // open inline editor
	if !d.editing {
		t.Fatal("enter on a text row should open the editor")
	}
	typeStr(d, "qwen3")
	d.handleKey(skey(tea.KeyEnter)) // commit
	if d.editing {
		t.Fatal("enter should commit and close the editor")
	}

	// Persisted at workspace scope, visible through the same resolution the
	// runtime uses.
	if got := s.ActiveProvider(t.Context(), engine.ProviderSpec{Model: "x"}); got.Model != "qwen3" {
		t.Fatalf("model not persisted: %q", got.Model)
	}
	if r := d.curRow(); !r.isSet || r.scope != prefs.ScopeWorkspace {
		t.Fatalf("row after edit: set=%v scope=%v, want set/workspace", r.isSet, r.scope)
	}

	// Promote workspace → global.
	d.handleKey(tkey("p"))
	if r := d.curRow(); r.scope != prefs.ScopeGlobal {
		t.Errorf("after promote: scope=%v, want global", r.scope)
	}
}

func TestSettingsDialog_RejectsNonNumericLimit(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)

	// Navigate to the budget category, reserve-tokens row.
	for d.cats[d.cat].name != "budget" && d.cat < len(d.cats)-1 {
		d.handleKey(skey(tea.KeyDown))
	}
	if d.cats[d.cat].name != "budget" {
		t.Fatalf("could not reach budget, at %q", d.cats[d.cat].name)
	}
	d.handleKey(skey(tea.KeyTab)) // focus rows (reserve tokens)
	if d.curRow().key != prefs.KeyReserveTokens {
		t.Fatalf("expected reserve row, got %q", d.curRow().key)
	}
	d.handleKey(skey(tea.KeyEnter))
	typeStr(d, "lots")
	d.handleKey(skey(tea.KeyEnter)) // commit — should be rejected

	if r := d.curRow(); r.isSet {
		t.Errorf("non-numeric reserve should not persist, but row isSet (value %q)", r.value)
	}
}

// Switching the provider must reset the model to the new provider's default
// so we never strand an incompatible cross-provider model (deepseek + opus).
func TestSettingsDialog_ProviderChangeResetsModel(t *testing.T) {
	ctx := t.Context()
	s := newTestSettings(t)
	// Pre-existing model from a previous (anthropic) selection.
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyModel, "claude-opus-4-7"); err != nil {
		t.Fatal(err)
	}
	d := newSettingsDialog(s)

	// Pin the provider row to deepseek, then trigger the change handler.
	if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyProvider, "deepseek"); err != nil {
		t.Fatal(err)
	}
	d.refresh(ctx)
	if d.currentProvider() != "deepseek" {
		t.Fatalf("provider not deepseek: %q", d.currentProvider())
	}
	d.onProviderCycled()

	got := s.Setting(ctx, prefs.KeyModel, "")
	if got == "claude-opus-4-7" {
		t.Error("model still the old anthropic model after switching to deepseek")
	}
	if got != "deepseek-chat" { // deepseek's DefaultModel
		t.Errorf("model = %q, want deepseek's default (deepseek-chat)", got)
	}
}

func TestSettingsDialog_EnumCyclePersists(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)

	d.handleKey(skey(tea.KeyTab)) // focus rows; row 0 = provider (opens a list picker)
	if d.curRow().key != prefs.KeyProvider {
		t.Fatalf("row 0 = %q, want provider", d.curRow().key)
	}
	act := d.handleKey(skey(tea.KeyEnter)) // opens list picker
	push, ok := act.(actionPush)
	if !ok {
		t.Fatalf("provider enter should open a list picker, got %T", act)
	}
	lp, ok := push.d.(*listPicker)
	if !ok {
		t.Fatalf("provider picker should be *listPicker, got %T", push.d)
	}
	if len(lp.items) < 2 {
		t.Fatal("need at least 2 providers to test")
	}
	// Move off the current provider and select a different one.
	for lp.items[lp.cursor] == d.currentProvider() {
		lp.handleKey(skey(tea.KeyDown))
	}
	lp.handleKey(skey(tea.KeyEnter)) // commit selection

	r := d.curRow()
	if !r.isSet || r.scope != prefs.ScopeWorkspace {
		t.Fatalf("provider picker commit should persist at workspace: set=%v scope=%v", r.isSet, r.scope)
	}
	if r.value == "" {
		t.Error("provider picker committed an empty provider")
	}
}

func TestSettingsDialog_SandboxTogglePersists(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	if !gotoCat(d, "behavior") {
		t.Fatal("behavior category missing")
	}
	d.handleKey(skey(tea.KeyTab))
	for d.curRow().key != prefs.KeySandbox && d.row < len(d.rows())-1 {
		d.handleKey(skey(tea.KeyDown))
	}
	if d.curRow().key != prefs.KeySandbox {
		t.Fatalf("could not reach sandbox row, at %q", d.curRow().key)
	}

	d.handleKey(skey(tea.KeyEnter)) // on -> off
	if got := s.Setting(t.Context(), prefs.KeySandbox, ""); got != "off" {
		t.Fatalf("sandbox setting = %q, want off", got)
	}
	if r := d.curRow(); !r.isSet || r.scope != prefs.ScopeWorkspace || r.value != "off" {
		t.Fatalf("sandbox row after toggle: set=%v scope=%v value=%q", r.isSet, r.scope, r.value)
	}
}

// Codex effort used to be a stuck enum (its "(auto)" default wasn't in the
// option list, so cycling never advanced past the empty entry). It now opens a
// list picker; committing a real level persists it.
func TestSettingsDialog_CodexEffortPicker(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	if !gotoCat(d, "model") { // codex effort now lives under Model
		t.Fatal("model category missing")
	}
	d.handleKey(skey(tea.KeyTab)) // focus rows
	for d.curRow().key != prefs.KeyCodexEffort && d.row < len(d.rows())-1 {
		d.handleKey(skey(tea.KeyDown))
	}
	if d.curRow().key != prefs.KeyCodexEffort {
		t.Fatalf("could not reach codex effort row, at %q", d.curRow().key)
	}

	push, ok := d.handleKey(skey(tea.KeyEnter)).(actionPush)
	if !ok {
		t.Fatalf("enter on codex effort should open a picker")
	}
	lp, ok := push.d.(*listPicker)
	if !ok {
		t.Fatalf("codex effort should push a *listPicker, got %T", push.d)
	}
	for lp.cursor < len(lp.items)-1 && lp.items[lp.cursor] != "high" {
		lp.handleKey(skey(tea.KeyDown))
	}
	if lp.items[lp.cursor] != "high" {
		t.Fatalf("could not select high, at %q", lp.items[lp.cursor])
	}
	lp.handleKey(skey(tea.KeyEnter)) // onPick → commit
	if got := s.Setting(t.Context(), prefs.KeyCodexEffort, ""); got != "high" {
		t.Errorf("codex effort = %q, want high", got)
	}
}

// Compaction engine is a selectable setting: enter opens a picker over the
// engine list and the choice persists.
func TestSettingsDialog_CompactionEnginePicker(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	if !gotoCat(d, "compaction") {
		t.Fatal("compaction category missing")
	}
	d.handleKey(skey(tea.KeyTab)) // focus rows; row 0 = engine
	if d.curRow().key != prefs.KeyCompactEngine {
		t.Fatalf("row 0 = %q, want compact engine", d.curRow().key)
	}
	push, ok := d.handleKey(skey(tea.KeyEnter)).(actionPush)
	if !ok {
		t.Fatalf("enter on engine should open a picker")
	}
	lp, ok := push.d.(*listPicker)
	if !ok {
		t.Fatalf("engine should push a *listPicker, got %T", push.d)
	}
	for lp.cursor < len(lp.items)-1 && lp.items[lp.cursor] != "summary" {
		lp.handleKey(skey(tea.KeyDown))
	}
	if lp.items[lp.cursor] != "summary" {
		t.Fatalf("could not select summary, at %q", lp.items[lp.cursor])
	}
	lp.handleKey(skey(tea.KeyEnter))
	if got := s.Setting(t.Context(), prefs.KeyCompactEngine, ""); got != "summary" {
		t.Errorf("compact engine = %q, want summary", got)
	}
}

// The compaction provider is a picker (not free text): it offers an (active)
// sentinel plus the provider list, persists the pick, and queues a model fetch
// for the chosen backend.
func TestSettingsDialog_CompactionProviderPicker(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	if !gotoCat(d, "compaction") {
		t.Fatal("compaction category missing")
	}
	d.handleKey(skey(tea.KeyTab))  // focus rows; row 0 = engine
	d.handleKey(skey(tea.KeyDown)) // → provider
	if d.curRow().key != prefs.KeyCompactProvider {
		t.Fatalf("expected compact provider row, got %q", d.curRow().key)
	}
	push, ok := d.handleKey(skey(tea.KeyEnter)).(actionPush)
	if !ok {
		t.Fatalf("enter on compaction provider should open a picker")
	}
	lp, ok := push.d.(*listPicker)
	if !ok {
		t.Fatalf("want *listPicker, got %T", push.d)
	}
	if lp.items[0] != compactActiveSentinel {
		t.Errorf("first item = %q, want %q", lp.items[0], compactActiveSentinel)
	}
	target := lp.items[1] // first real provider
	for lp.cursor < len(lp.items)-1 && lp.items[lp.cursor] != target {
		lp.handleKey(skey(tea.KeyDown))
	}
	lp.handleKey(skey(tea.KeyEnter))
	if got := s.Setting(t.Context(), prefs.KeyCompactProvider, ""); got != target {
		t.Errorf("compact provider = %q, want %q", got, target)
	}
	if d.pendingFetch != target {
		t.Errorf("picking a provider should queue a fetch for it: pendingFetch=%q, want %q", d.pendingFetch, target)
	}
}

// The compaction model picker resolves against the compaction provider, not
// the active one.
func TestSettingsDialog_CompactionModelUsesCompactProvider(t *testing.T) {
	s := newTestSettings(t)
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyCompactProvider, "anthropic"); err != nil {
		t.Fatal(err)
	}
	d := newSettingsDialog(s)
	if d.compactProvider() != "anthropic" {
		t.Fatalf("compactProvider = %q, want anthropic", d.compactProvider())
	}
	d.models["anthropic"] = []string{"claude-test"} // seed a fetched list

	gotoCat(d, "compaction")
	d.handleKey(skey(tea.KeyTab))  // engine
	d.handleKey(skey(tea.KeyDown)) // provider
	d.handleKey(skey(tea.KeyDown)) // model
	if d.curRow().key != prefs.KeyCompactModel {
		t.Fatalf("expected compact model row, got %q", d.curRow().key)
	}
	push, ok := d.handleKey(skey(tea.KeyEnter)).(actionPush)
	if !ok {
		t.Fatalf("enter on compaction model should open a picker")
	}
	lp, ok := push.d.(*listPicker)
	if !ok {
		t.Fatalf("want *listPicker, got %T", push.d)
	}
	if lp.title != "models · anthropic" {
		t.Errorf("picker title = %q, want %q", lp.title, "models · anthropic")
	}
	var sawModel, sawActive bool
	for _, it := range lp.items {
		switch it {
		case "claude-test":
			sawModel = true
		case compactActiveSentinel:
			sawActive = true
		}
	}
	if !sawModel || !sawActive {
		t.Errorf("compaction model picker items = %v, want claude-test + %q", lp.items, compactActiveSentinel)
	}
}

// The Appearance pane is an inline theme gallery: focusing it and moving
// previews live, and enter persists the highlighted theme.
func TestSettingsDialog_ThemeGalleryAppliesAndPersists(t *testing.T) {
	names := themeNames()
	if len(names) < 2 {
		t.Skip("need ≥2 builtin themes")
	}
	if t0, ok := theme.ByName(names[0]); ok {
		UseTheme(t0)
	}
	defer UseTheme(theme.Theme{})

	s := newTestSettings(t)
	d := newSettingsDialog(s)
	if !gotoCat(d, "appearance") {
		t.Fatal("appearance category missing")
	}
	if !d.cats[d.cat].gallery {
		t.Fatal("appearance should be flagged as the inline gallery")
	}
	// Render once so the gallery learns its column count for navigation.
	d.draw(uvScreen(120, 30), uvRect(120, 30))

	d.handleKey(skey(tea.KeyTab)) // focus the gallery
	if !d.focusRows {
		t.Fatal("tab should focus the gallery")
	}
	d.handleKey(skey(tea.KeyRight)) // move → live preview applies
	picked := d.gallery.names[d.gallery.cursor]
	if palette.Name != picked {
		t.Errorf("preview not applied live: palette=%q, want %q", palette.Name, picked)
	}

	d.handleKey(skey(tea.KeyEnter)) // commit → persist
	if got := s.Setting(t.Context(), prefs.KeyTheme, ""); got != picked {
		t.Errorf("theme not persisted: got %q, want %q", got, picked)
	}
}

// Leaving the gallery with esc (no commit) reverts the live preview.
func TestSettingsDialog_ThemeGalleryRevertsOnEsc(t *testing.T) {
	names := themeNames()
	if len(names) < 2 {
		t.Skip("need ≥2 builtin themes")
	}
	if t0, ok := theme.ByName(names[0]); ok {
		UseTheme(t0)
	}
	defer UseTheme(theme.Theme{})
	origin := palette.Name

	s := newTestSettings(t)
	d := newSettingsDialog(s)
	gotoCat(d, "appearance")
	d.draw(uvScreen(120, 30), uvRect(120, 30))
	d.handleKey(skey(tea.KeyTab))   // focus
	d.handleKey(skey(tea.KeyRight)) // preview a different theme
	if palette.Name == origin {
		t.Skip("cursor did not move")
	}
	d.handleKey(skey(tea.KeyEscape)) // leave without committing → revert
	if palette.Name != origin {
		t.Errorf("esc should revert preview to %q, got %q", origin, palette.Name)
	}
	if d.focusRows {
		t.Error("esc should return focus to the nav")
	}
}

// esc in the theme picker reverts the live preview to the theme active when it
// opened, leaving nothing persisted.
func TestSettingsDialog_ThemePickerRevertsOnCancel(t *testing.T) {
	names := themeNames()
	if len(names) < 2 {
		t.Skip("need ≥2 builtin themes")
	}
	if t0, ok := theme.ByName(names[0]); ok {
		UseTheme(t0)
	}
	defer UseTheme(theme.Theme{})

	tp := newThemePickerFor(func(string) { t.Fatal("onPick must not fire on cancel") })
	tp.handleKey(skey(tea.KeyDown)) // preview a different theme
	if palette.Name == names[0] {
		t.Skip("cursor did not move")
	}
	tp.handleKey(skey(tea.KeyEscape)) // revert
	if palette.Name != names[0] {
		t.Errorf("esc should revert preview to %q, got %q", names[0], palette.Name)
	}
}
