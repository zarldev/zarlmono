package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// gotoRow focuses the detail rows and moves the cursor to the row with the
// given setting key within the current category.
func gotoRow(d *settingsDialog, key string) bool {
	d.focusRows = true
	for i, r := range d.rows() {
		if r.key == key {
			d.row = i
			return true
		}
	}
	return false
}

func TestSettingsDialog_ProviderChangeRequestsModelFetch(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)

	// Focus the provider row and open the list picker — selecting a different
	// provider should queue a model fetch and reset the model.
	if !gotoRow(d, prefs.KeyProvider) {
		t.Fatal("provider row missing")
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
	// Pick the second provider (different from current).
	if len(lp.items) < 2 {
		t.Fatal("need at least 2 providers to test a change")
	}
	// Move to the second provider and select it.
	for lp.items[lp.cursor] == d.currentProvider() {
		lp.handleKey(skey(tea.KeyDown))
	}
	lp.handleKey(skey(tea.KeyEnter)) // commit selection
	if !d.modelsLoading[d.currentProvider()] {
		t.Error("provider should be marked loading after switching via picker")
	}
}

func TestSettingsDialog_ModelPickerFromFetchedList(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	p := d.currentProvider()

	// No models yet → enter on the model row drops to free-text entry.
	gotoRow(d, prefs.KeyModel)
	d.handleKey(skey(tea.KeyEnter))
	if !d.editing {
		t.Fatal("with no fetched models, model row should open the text editor")
	}
	d.handleKey(skey(tea.KeyEscape)) // cancel edit

	// Simulate a completed fetch.
	d.onModelsLoaded(p, []string{"qwen3", "gpt-4o-mini", "sonnet"}, nil)
	if d.modelsLoading[p] {
		t.Error("loading flag should clear after onModelsLoaded")
	}

	// Now enter on the model row opens the picker (a list dialog).
	gotoRow(d, prefs.KeyModel)
	act := d.handleKey(skey(tea.KeyEnter))
	push, ok := act.(actionPush)
	if !ok {
		t.Fatalf("model row with a fetched list should open a picker, got %T", act)
	}
	lp, ok := push.d.(*listPicker)
	if !ok {
		t.Fatalf("expected a listPicker, got %T", push.d)
	}
	// Sentinel (custom entry) + the three fetched models.
	if len(lp.items) != 4 || lp.items[0] != modelCustomSentinel {
		t.Fatalf("picker items wrong: %v", lp.items)
	}

	// Picking a model commits it at workspace scope.
	lp.cursor = 2 // "gpt-4o-mini" (index 0 is the sentinel)
	if pick := lp.handleKey(skey(tea.KeyEnter)); pick == nil {
		t.Fatal("picker enter returned nil action")
	}
	if got, ok, _ := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyModel); !ok || got.Value != "gpt-4o-mini" {
		t.Errorf("model not committed from picker: ok=%v val=%q", ok, got.Value)
	}
}

func TestSettingsDialog_ModelPickerCustomEntry(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	p := d.currentProvider()
	d.onModelsLoaded(p, []string{"qwen3"}, nil)

	gotoRow(d, prefs.KeyModel)
	act := d.handleKey(skey(tea.KeyEnter))
	lp := act.(actionPush).d.(*listPicker)

	lp.cursor = 0 // the custom sentinel
	lp.handleKey(skey(tea.KeyEnter))
	if !d.editing {
		t.Error("picking the custom sentinel should open the text editor")
	}
}

func TestSettingsDialog_CodexEffortOptionsFollowModel(t *testing.T) {
	s := newTestSettings(t)
	d := newSettingsDialog(s)
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProvider, backends.NameOpenAICodex.String()); err != nil {
		t.Fatal(err)
	}
	if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyModel, "gpt-5.3-codex-spark"); err != nil {
		t.Fatal(err)
	}
	d.refresh(t.Context())
	if !gotoRow(d, prefs.KeyCodexEffort) {
		t.Fatal("reasoning effort row missing")
	}
	push, ok := d.handleKey(skey(tea.KeyEnter)).(actionPush)
	if !ok {
		t.Fatalf("enter on reasoning effort should open a picker")
	}
	lp := push.d.(*listPicker)
	want := []string{codexEffortAuto, "low"}
	if strings.Join(lp.items, ",") != strings.Join(want, ",") {
		t.Fatalf("items = %v, want %v", lp.items, want)
	}
}
