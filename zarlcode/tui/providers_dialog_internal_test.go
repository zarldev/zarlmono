package tui

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// newTestSettingsWithVault assembles a engine.Settings backed by a throwaway db
// AND a real vault (master key from ZARLCODE_KEY, no file written), so
// the encrypted api-key path can be exercised.
func newTestSettingsWithVault(t *testing.T) *engine.Settings {
	t.Helper()
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	dir := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return engine.NewSettings(t.Context(), store, v, dir)
}

type keyHandler interface {
	handleKey(tea.KeyPressMsg) action
}

func typeKeys(h keyHandler, s string) {
	for _, ch := range s {
		h.handleKey(tea.KeyPressMsg{Text: string(ch), Code: ch})
	}
}

func providerExists(s *engine.Settings, name string) bool {
	for _, def := range s.Registry.All() {
		if def.Name == name {
			return true
		}
	}
	return false
}

func gotoProvider(d *providersDialog, name string) bool {
	for i, def := range d.defs {
		if def.Name == name {
			d.cursor = i
			return true
		}
	}
	return false
}

func TestProvidersDialog_AddCustomInline(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)

	d.handleKey(tkey("n")) // open the inline add form
	if !d.adding {
		t.Fatal("`n` should open the inline add form")
	}
	typeKeys(d, "mylocal")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "http://localhost:9000/v1")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "qwen3")
	d.handleKey(skey(tea.KeyTab))   // → reasoning
	d.handleKey(skey(tea.KeyTab))   // → context window
	d.handleKey(skey(tea.KeyTab))   // → price
	d.handleKey(skey(tea.KeyEnter)) // submit on last field (blank → defaults)

	if d.adding {
		t.Error("a successful add should close the form")
	}
	if !providerExists(s, "mylocal") {
		t.Error("custom provider was not added to the registry")
	}
}

func TestProvidersDialog_AddCustomPersistsReasoningField(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)

	d.handleKey(tkey("n"))
	typeKeys(d, "kimi")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "https://api.moonshot.ai/v1")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "kimi-k2-thinking")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "field")          // thinking model: reasoning_content echoed back
	d.handleKey(skey(tea.KeyTab)) // → context window
	d.handleKey(skey(tea.KeyTab)) // → price
	d.handleKey(skey(tea.KeyEnter))

	if d.adding {
		t.Fatalf("add should close on success: %s", d.status)
	}
	var got llm.ReasoningHistory
	var found bool
	for _, def := range s.Registry.All() {
		if def.Name == "kimi" {
			got, found = def.ReasoningHistory, true
		}
	}
	if !found {
		t.Fatal("kimi provider not found in registry")
	}
	if got != llm.ReasoningHistories.FIELD {
		t.Fatalf("reasoning_history = %v, want field", got)
	}
}

func TestProvidersDialog_EditCustomUpdatesReasoning(t *testing.T) {
	s := newTestSettingsWithVault(t)
	if err := s.Registry.UpsertProvider(t.Context(), backends.ProviderDefinition{
		Name: "kimi", DisplayName: "kimi",
		AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:     "https://api.moonshot.ai/v1",
		Enabled:     true, // seeded with default (inline) reasoning
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d := newProvidersDialog(s)
	if !gotoProvider(d, "kimi") {
		t.Fatal("seeded provider missing")
	}

	d.handleKey(tkey("e")) // open the edit form, pre-filled
	if !d.adding || d.editOrig != "kimi" {
		t.Fatalf("e should open the edit form for kimi; adding=%v editOrig=%q", d.adding, d.editOrig)
	}
	if d.addEds[0].text() != "kimi" || d.addEds[1].text() != "https://api.moonshot.ai/v1" {
		t.Fatalf("edit form not pre-filled: name=%q url=%q", d.addEds[0].text(), d.addEds[1].text())
	}

	// Change reasoning to "field" and submit (last field).
	d.addEds[3] = newComposer("field")
	d.addIdx = 5 // last field, so Enter submits
	d.handleKey(skey(tea.KeyEnter))

	if d.adding {
		t.Fatalf("edit should close on success: %s", d.status)
	}
	var got llm.ReasoningHistory
	for _, def := range s.Registry.All() {
		if def.Name == "kimi" {
			got = def.ReasoningHistory
		}
	}
	if got != llm.ReasoningHistories.FIELD {
		t.Fatalf("reasoning_history = %v, want field after edit", got)
	}
}

func TestProvidersDialog_AddCustomPersistsContextWindow(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)

	d.handleKey(tkey("n"))
	typeKeys(d, "kimi")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "https://api.moonshot.ai/v1")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "kimi-k2.6")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "field")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "262144")         // Kimi K2.x window
	d.handleKey(skey(tea.KeyTab)) // → price
	d.handleKey(skey(tea.KeyEnter))

	if d.adding {
		t.Fatalf("add should close on success: %s", d.status)
	}
	var got int
	for _, def := range s.Registry.All() {
		if def.Name == "kimi" {
			got = def.ContextWindow
		}
	}
	if got != 262144 {
		t.Fatalf("context_window = %d, want 262144", got)
	}
	// The explicit window wins in resolution (no static table entry for kimi).
	if cw := s.Registry.ContextWindow("kimi", "kimi-k2.6"); cw != 262144 {
		t.Fatalf("registry ContextWindow = %d, want 262144", cw)
	}
}

func TestProvidersDialog_AddCustomPersistsCost(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)

	d.handleKey(tkey("n"))
	typeKeys(d, "kimi")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "https://api.moonshot.ai/v1")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "kimi-k2.6")
	d.handleKey(skey(tea.KeyTab)) // → reasoning (skip)
	d.handleKey(skey(tea.KeyTab)) // → context window (skip)
	d.handleKey(skey(tea.KeyTab)) // → price
	typeKeys(d, "0.6/2.5")        // USD per 1M in/out
	d.handleKey(skey(tea.KeyEnter))

	if d.adding {
		t.Fatalf("add should close on success: %s", d.status)
	}
	var def backends.ProviderDefinition
	for _, dd := range s.Registry.All() {
		if dd.Name == "kimi" {
			def = dd
		}
	}
	if def.InputCostPerMTok != 0.6 || def.OutputCostPerMTok != 2.5 {
		t.Fatalf("cost = %v/%v per M, want 0.6/2.5", def.InputCostPerMTok, def.OutputCostPerMTok)
	}
	// Registry.Cost reports per-1k and metered (ok) from the explicit price.
	in, out, ok := s.Registry.Cost("kimi", "kimi-k2.6")
	if !ok || in <= 0 || out <= 0 {
		t.Fatalf("Cost = %v/%v ok=%v, want metered", in, out, ok)
	}
}

func TestProvidersDialog_AddCustomRejectsBadURL(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)

	d.handleKey(tkey("n"))
	typeKeys(d, "broken")
	d.handleKey(skey(tea.KeyTab))
	typeKeys(d, "not-a-url")
	d.handleKey(skey(tea.KeyTab)) // to default model
	d.handleKey(skey(tea.KeyTab)) // to reasoning
	d.handleKey(skey(tea.KeyTab)) // to context window
	d.handleKey(skey(tea.KeyTab)) // to price
	d.handleKey(skey(tea.KeyEnter))

	if !d.adding {
		t.Error("invalid base URL should keep the form open")
	}
	if d.status == "" {
		t.Error("expected a validation error status")
	}
	if providerExists(s, "broken") {
		t.Error("invalid provider should not be persisted")
	}
}

func TestProvidersDialog_SetActiveAndEditKey(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)
	if !gotoProvider(d, "openai") {
		t.Fatal("openai builtin missing")
	}

	d.handleKey(tkey("a")) // set active
	if sv, ok, _ := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyProvider); !ok || sv.Value != "openai" {
		t.Errorf("active provider not persisted: ok=%v val=%q", ok, sv.Value)
	}

	d.handleKey(skey(tea.KeyEnter)) // start key edit (openai uses a key)
	if !d.editing {
		t.Fatal("enter should open the api-key editor for a key-using provider")
	}
	typeKeys(d, "sk-test-123")
	d.handleKey(skey(tea.KeyEnter)) // commit

	if k, ok, _ := s.Svc.GetKey(t.Context(), prefs.ScopeGlobal, "openai"); !ok || k != "sk-test-123" {
		t.Errorf("api key not stored in vault: ok=%v key=%q", ok, k)
	}
	if !d.hasKey["openai"] {
		t.Error("key status not refreshed after save")
	}
}

func TestProvidersDialog_PasteIntoKeyEditor(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)
	if !gotoProvider(d, "openai") {
		t.Fatal("openai builtin missing")
	}
	d.handleKey(skey(tea.KeyEnter)) // open the api-key editor
	if !d.editing {
		t.Fatal("enter should open the api-key editor")
	}
	typeKeys(d, "sk-")           // a few typed runes…
	d.handlePaste("pasted-rest") // …then a paste lands at the cursor
	if got := d.editor.text(); got != "sk-pasted-rest" {
		t.Fatalf("editor after paste = %q, want sk-pasted-rest", got)
	}
}

func TestProvidersDialog_PasteIntoAddForm(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newProvidersDialog(s)
	d.handleKey(tkey("n")) // open the add form (cursor on the name field)
	d.handlePaste("kimi")
	if got := d.addEds[0].text(); got != "kimi" {
		t.Fatalf("name field after paste = %q, want kimi", got)
	}
}

// TestSettingsDialog_PasteRoutesToProviderKey drives the full overlay path:
// the settings surface strips newlines and forwards the paste to the focused
// providers panel's key editor — the exact flow that pasting an API key uses.
func TestSettingsDialog_PasteRoutesToProviderKey(t *testing.T) {
	s := newTestSettingsWithVault(t)
	d := newSettingsDialog(s)

	providersCat := -1
	for i, c := range d.cats {
		if c.providers {
			providersCat = i
		}
	}
	if providersCat < 0 {
		t.Fatal("providers category missing")
	}
	d.cat = providersCat
	d.focusRows = true
	if !gotoProvider(d.providers, "openai") {
		t.Fatal("openai builtin missing")
	}
	d.handleKey(skey(tea.KeyEnter)) // start the key editor inside the panel
	if !d.providers.editing {
		t.Fatal("enter should open the providers key editor")
	}

	d.handlePaste("sk-line1\nsk-line2\n") // trailing/embedded newlines must be stripped
	if got := d.providers.editor.text(); got != "sk-line1sk-line2" {
		t.Fatalf("key editor after paste = %q, want sk-line1sk-line2", got)
	}
}

func TestProvidersDialog_DeleteRules(t *testing.T) {
	s := newTestSettingsWithVault(t)
	if err := s.Registry.UpsertProvider(t.Context(), backends.ProviderDefinition{
		Name: "tmp", DisplayName: "tmp",
		AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:     "http://x.example/v1", Enabled: true,
	}); err != nil {
		t.Fatalf("seed custom provider: %v", err)
	}
	d := newProvidersDialog(s)

	gotoProvider(d, "llamacpp")
	d.handleKey(tkey("x")) // built-in: must be rejected
	if !providerExists(s, "llamacpp") {
		t.Error("built-in provider must not be deletable")
	}

	if !gotoProvider(d, "tmp") {
		t.Fatal("seeded custom provider missing")
	}
	d.handleKey(tkey("x")) // custom: removed
	if providerExists(s, "tmp") {
		t.Error("custom provider was not deleted")
	}
}
