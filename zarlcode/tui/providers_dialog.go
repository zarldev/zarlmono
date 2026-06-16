package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

const providerModelFetchTimeout = 6 * time.Second

// providersDialog lists every provider (built-in + custom) and manages
// them: set the active one, edit its vault-stored API key, fetch its model
// list, add a custom OpenAI-compatible provider, or delete a custom one.
// API keys are account-level credentials, so they're stored at global scope.
type providersDialog struct {
	ctx    context.Context
	s      *engine.Settings
	defs   []backends.ProviderDefinition
	hasKey map[string]bool
	active string
	cursor int

	editing  bool // editing the selected provider's API key
	editor   composer
	status   string
	statusAt time.Time // when status was last set, for the host's footer toast

	adding   bool        // inline add/edit custom-provider form is open
	addEds   [6]composer // name, base URL, default model, reasoning, context window, price/M
	addIdx   int
	editOrig string // non-empty when the form is editing this existing provider

	oauthBusy bool   // an OAuth sign-in is awaiting the browser callback
	oauthURL  string // the sign-in URL (shown for manual copy)
}

var providerAddLabels = [6]string{"name", "base url", "default model", "reasoning", "context window", "price /M (in/out)"}

// inSubMode reports whether the panel is in a text-entry sub-mode (editing a
// key or filling the add form) — the host settings pane uses this to know
// when esc/left should cancel the sub-mode vs return focus to the nav.
func (d *providersDialog) inSubMode() bool { return d.editing || d.adding }

// isOAuthProvider reports whether a provider authenticates via OAuth (a
// browser sign-in + token source) rather than a static API key.
func isOAuthProvider(name string) bool {
	id, err := llm.ParseLLMProvider(name)
	if err != nil {
		return false
	}
	return id == backends.NameOpenAICodex || id == backends.NameClaudeCode
}

// beginOAuth puts the dialog into the awaiting-callback state with the
// sign-in URL on display.
func (d *providersDialog) beginOAuth(url string) {
	d.oauthBusy = true
	d.oauthURL = url
	d.status, d.statusAt = "opening browser to sign in…", time.Now()
}

// onOAuthResult clears the awaiting state and reports the outcome.
func (d *providersDialog) onOAuthResult(account string, err error) {
	d.oauthBusy = false
	d.oauthURL = ""
	switch {
	case err != nil:
		d.status = "sign-in failed: " + err.Error()
	case account != "":
		d.status = "signed in (account " + account + ")"
	default:
		d.status = "signed in"
	}
	d.statusAt = time.Now()
	d.refresh()
}

// footerHint is the key legend the host surface shows in its footer while the
// providers pane is focused — sub-mode aware.
func (d *providersDialog) footerHint() string {
	switch {
	case d.editing:
		return keyLegend(keyHint{label: "type key"}, keyHint{"enter", "save"}, keyHint{"esc", "cancel"})
	case d.adding:
		return keyLegend(keyHint{"tab", "field"}, keyHint{"enter", "next/save"}, keyHint{"esc", "cancel"})
	case d.oauthBusy:
		return keyLegend(keyHint{label: "waiting for the browser callback…"}, keyHint{"esc", "cancel"})
	default:
		return keyLegend(keyHint{"↵", "key/sign-in"}, keyHint{"a", "active"}, keyHint{"m", "models"},
			keyHint{"n", "new"}, keyHint{"e", "edit"}, keyHint{"x", "delete"}, keyHint{"esc", "back"})
	}
}

func newProvidersDialog(s *engine.Settings) *providersDialog {
	return newProvidersDialogWithContext(context.Background(), s)
}

func newProvidersDialogWithContext(ctx context.Context, s *engine.Settings) *providersDialog {
	if ctx == nil {
		ctx = context.Background()
	}
	d := &providersDialog{ctx: ctx, s: s, hasKey: map[string]bool{}}
	d.refresh()
	return d
}

func (d *providersDialog) refresh() {
	if d.s == nil || d.s.Registry == nil {
		return
	}
	ctx := d.ctx
	// Reload the merged view from the store: registry.Delete persists but
	// doesn't refresh the in-memory catalogue (unlike UpsertProvider), so
	// reload here keeps the list truthful after a delete.
	_ = d.s.Registry.Reload(ctx)
	d.defs = d.s.Registry.All()
	d.hasKey = map[string]bool{}
	if d.s.Svc != nil {
		if names, err := d.s.Svc.ListKeys(ctx, prefs.ScopeEffective); err == nil {
			for _, n := range names {
				d.hasKey[n] = true
			}
		}
		if sv, ok, _ := d.s.Svc.GetSetting(ctx, prefs.ScopeEffective, prefs.KeyProvider); ok {
			d.active = sv.Value
		}
	}
	if d.cursor >= len(d.defs) {
		d.cursor = len(d.defs) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

func (d *providersDialog) cur() backends.ProviderDefinition {
	if d.cursor < 0 || d.cursor >= len(d.defs) {
		return backends.ProviderDefinition{}
	}
	return d.defs[d.cursor]
}

// handleKey runs the inner key logic and timestamps any resulting status
// change so the host surface's footer toast can age it out.
func (d *providersDialog) handleKey(msg tea.KeyPressMsg) action {
	before := d.status
	a := d.handleKeyInner(msg)
	if d.status != before {
		d.statusAt = time.Now()
	}
	return a
}

func (d *providersDialog) handleKeyInner(msg tea.KeyPressMsg) action {
	if d.editing {
		return d.handleKeyEdit(msg)
	}
	if d.adding {
		return d.handleAddKey(msg)
	}
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(d.defs) {
			d.cursor++
		}
	case "enter":
		if d.cursor == len(d.defs) {
			d.openAddForm()
			return actionNone{}
		}
		if isOAuthProvider(d.cur().Name) {
			return d.startSignIn()
		}
		d.startKeyEdit()
	case "e":
		if d.cursor < len(d.defs) {
			d.startEditCustom()
		}
	case "a":
		if d.cursor < len(d.defs) {
			d.setActive()
		}
	case "m":
		if d.cursor < len(d.defs) {
			d.fetchModels()
		}
	case "n":
		d.openAddForm()
	case "x", "delete":
		if d.cursor < len(d.defs) {
			d.deleteCustom()
		}
	}
	return actionNone{}
}

// handleAddKey drives the inline add/edit-custom-provider form.
func (d *providersDialog) handleAddKey(msg tea.KeyPressMsg) action {
	return handleAddFormKey(msg, d.addEds[:], &d.addIdx, func() { d.adding = false; d.editOrig = "" }, d.submitAdd)
}

// openAddForm opens the inline form blank, for adding a new provider.
func (d *providersDialog) openAddForm() {
	d.adding = true
	d.editOrig = ""
	d.addEds = [6]composer{}
	d.addIdx = 0
	d.status = ""
}

// parseCostPerM parses a "in/out" price string (USD per 1M tokens). Blank or
// unparseable yields 0/0 (unmetered).
func parseCostPerM(s string) (float64, float64) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	var in, out float64
	parts := strings.SplitN(s, "/", 2)
	in, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if len(parts) == 2 {
		out, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	}
	if in < 0 {
		in = 0
	}
	if out < 0 {
		out = 0
	}
	return in, out
}

// formatCostPerM renders an "in/out" price string for the edit form, or "" when
// both are zero.
func formatCostPerM(in, out float64) string {
	if in == 0 && out == 0 {
		return ""
	}
	return strconv.FormatFloat(in, 'g', -1, 64) + "/" + strconv.FormatFloat(out, 'g', -1, 64)
}

// newComposer returns a text field pre-filled with s (cursor at end).
func newComposer(s string) composer {
	r := []rune(s)
	return composer{value: r, cursor: len(r)}
}

// startEditCustom opens the inline form pre-filled with the selected custom
// provider's fields. Built-ins aren't editable.
func (d *providersDialog) startEditCustom() {
	def := d.cur()
	if def.Builtin {
		d.status = "built-in providers can't be edited"
		return
	}
	d.adding = true
	d.editOrig = def.Name
	win := ""
	if def.ContextWindow > 0 {
		win = strconv.Itoa(def.ContextWindow)
	}
	d.addEds = [6]composer{
		newComposer(def.Name),
		newComposer(def.BaseURL),
		newComposer(def.DefaultModel),
		newComposer(def.ReasoningHistory.String()),
		newComposer(win),
		newComposer(formatCostPerM(def.InputCostPerMTok, def.OutputCostPerMTok)),
	}
	d.addIdx = 0
	d.status = ""
}

// submitAdd upserts the custom OpenAI-compatible provider from the inline
// form and, on success, closes the form and refreshes the list.
func (d *providersDialog) submitAdd() {
	if d.s == nil || d.s.Registry == nil {
		d.adding = false
		return
	}
	name := strings.TrimSpace(d.addEds[0].text())
	// Reasoning policy is an enum; blank or unrecognised input defaults to
	// INLINE (today's behaviour). Thinking models (Moonshot/Kimi) take "field".
	reasoning, err := llm.ParseReasoningHistory(strings.TrimSpace(d.addEds[3].text()))
	if err != nil {
		reasoning = llm.ReasoningHistories.INLINE
	}
	// Context window is optional; blank or non-numeric means "auto" (0 →
	// fall back to the table/probe/default).
	window, _ := strconv.Atoi(strings.TrimSpace(d.addEds[4].text()))
	if window < 0 {
		window = 0
	}
	inCost, outCost := parseCostPerM(d.addEds[5].text())
	def := backends.ProviderDefinition{
		Name:              name,
		DisplayName:       name,
		AdapterType:       backends.AdapterTypes.OPENAICOMPATIBLE,
		BaseURL:           strings.TrimSpace(d.addEds[1].text()),
		DefaultModel:      strings.TrimSpace(d.addEds[2].text()),
		ReasoningHistory:  reasoning,
		ContextWindow:     window,
		InputCostPerMTok:  inCost,
		OutputCostPerMTok: outCost,
		Enabled:           true,
		Builtin:           false,
	}
	if err := d.s.Registry.UpsertProvider(d.ctx, def); err != nil {
		d.status = "save: " + err.Error()
		return
	}
	// An edit that renamed the provider upserts a new row, so drop the old
	// one. Its vault-stored key (if any) is keyed by name and is left behind —
	// re-enter the key under the new name.
	if d.editOrig != "" && d.editOrig != name {
		if err := d.s.Registry.Delete(d.ctx, d.editOrig); err != nil {
			slog.WarnContext(d.ctx, "drop renamed provider's old row", "err", err, "old", d.editOrig, "new", name)
		}
	}
	verb := "added"
	if d.editOrig != "" {
		verb = "updated"
	}
	d.adding = false
	d.editOrig = ""
	d.status = name + " " + verb
	d.refresh()
}

// startSignIn requests an OAuth sign-in for the selected provider; the root
// turns the returned action into the browser + callback command.
func (d *providersDialog) startSignIn() action {
	if d.oauthBusy {
		return actionNone{}
	}
	return actionOAuthLogin{provider: d.cur().Name}
}

func (d *providersDialog) startKeyEdit() {
	def := d.cur()
	if !def.UsesAPIKey() {
		d.status = def.Name + " doesn't use an api key"
		return
	}
	if d.s == nil || d.s.Svc == nil {
		d.status = "credential service unavailable"
		return
	}
	d.editing = true
	d.editor = composer{}
}

func (d *providersDialog) handleKeyEdit(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc":
		d.editing = false
	case "enter":
		d.commitKey(strings.TrimSpace(d.editor.submit()))
		d.editing = false
	case "backspace":
		d.editor.backspace()
	case "left":
		d.editor.left()
	case "right":
		d.editor.right()
	default:
		if msg.Text != "" {
			d.editor.insert(msg.Text)
		}
	}
	return actionNone{}
}

// handlePaste inserts clipboard content into the active text field — the API
// key editor or the focused add-form field. Content is already single-line
// (the settings surface strips newlines before routing). No-op outside a
// text-entry sub-mode.
func (d *providersDialog) handlePaste(content string) {
	switch {
	case d.editing:
		d.editor.insert(content)
	case d.adding && d.addIdx >= 0 && d.addIdx < len(d.addEds):
		d.addEds[d.addIdx].insert(content)
	}
}

func (d *providersDialog) commitKey(val string) {
	name := d.cur().Name
	ctx := d.ctx
	switch val {
	case "":
		if err := d.s.Svc.DeleteKey(ctx, prefs.ScopeGlobal, name); err != nil {
			d.status = "clear key: " + err.Error()
		} else {
			d.status = name + " key cleared"
		}
	default:
		if err := d.s.Svc.SetKey(ctx, prefs.ScopeGlobal, name, val); err != nil {
			d.status = "save key: " + err.Error()
		} else {
			d.status = name + " key saved (global)"
		}
	}
	d.refresh()
}

func (d *providersDialog) setActive() {
	if d.s == nil || d.s.Svc == nil {
		return
	}
	name := d.cur().Name
	ctx := d.ctx
	if err := d.s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyProvider, name); err != nil {
		d.status = "set active: " + err.Error()
		return
	}
	// Repoint the model at the new provider's default so we don't strand a
	// model from the previous backend (e.g. deepseek + opus).
	d.s.ResetModelToProviderDefault(ctx, name)
	d.s.Registry.SetActiveName(name)
	d.status = name + " is the active provider (next run)"
	d.refresh()
}

func (d *providersDialog) fetchModels() {
	if d.s == nil || d.s.Registry == nil {
		return
	}
	name := d.cur().Name
	ctx, cancel := context.WithTimeout(d.ctx, providerModelFetchTimeout)
	defer cancel()
	models, err := d.s.Registry.FetchModels(ctx, name)
	if err != nil {
		d.status = "fetch models: " + err.Error()
		return
	}
	if len(models) == 0 {
		d.status = name + ": no models reported"
		return
	}
	preview := strings.Join(models[:min(len(models), 3)], ", ")
	if len(models) > 3 {
		preview += ", …"
	}
	d.status = fmt.Sprintf("%s: %d models (%s)", name, len(models), preview)
}

func (d *providersDialog) deleteCustom() {
	def := d.cur()
	if def.Builtin {
		d.status = "built-in providers can't be deleted"
		return
	}
	if err := d.s.Registry.Delete(d.ctx, def.Name); err != nil {
		d.status = "delete: " + err.Error()
		return
	}
	d.status = def.Name + " deleted"
	d.refresh()
}

// detailLines renders the provider list for embedding in the settings
// surface's detail region (the add form / sign-in view replace it while
// active). It returns rows only — the surface owns the footer hint + the
// status toast, so the list never grows its own.
func (d *providersDialog) detailLines(width int) []string {
	if d.adding {
		return d.addFormLines()
	}
	if d.oauthBusy {
		return d.signInLines(width)
	}
	lines := make([]string, 0, len(d.defs)+2)
	for i, def := range d.defs {
		marker := "  "
		namecell := palette.Subtle.On(pad(def.Name, 16))
		if i == d.cursor {
			marker = palette.Primary.On("▸ ")
			namecell = palette.Primary.On(pad(def.Name, 16))
		}
		if i == d.cursor && d.editing {
			bullets := strings.Repeat("•", len(d.editor.value))
			lines = append(lines, marker+namecell+"  key: "+bullets+palette.Primary.On("▏"))
			continue
		}
		lines = append(lines, rowLayout(marker+namecell, d.tags(def), width))
	}
	// "+ new provider" entry at the bottom of the list.
	newMarker := "  "
	newLabel := palette.Subtle.On(pad("+ new provider…", 16))
	if d.cursor == len(d.defs) {
		newMarker = palette.Primary.On("▸ ")
		newLabel = palette.Primary.On(pad("+ new provider…", 16))
	}
	lines = append(lines, newMarker+newLabel)
	return lines
}

// signInLines is the focused view shown while an OAuth sign-in awaits the
// browser callback: the full URL hard-wrapped so a long link stays visible
// (it's also on the clipboard) rather than truncated off-screen.
func (d *providersDialog) signInLines(width int) []string {
	lines := []string{
		palette.Assistant.On("signing in — your browser should open."),
		palette.Muted.On("if not, the url is on your clipboard, or type it from:"),
		"",
	}
	for _, ln := range hardWrap(d.oauthURL, width) {
		lines = append(lines, palette.Subtle.On(ln))
	}
	return lines
}

// hardWrap breaks s into width-rune chunks. Unlike wrapText it splits within
// a "word", so a URL (no spaces) wraps instead of overflowing/truncating.
func hardWrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	r := []rune(s)
	out := make([]string, 0, len(r)/width+1)
	for i := 0; i < len(r); i += width {
		out = append(out, string(r[i:min(i+width, len(r))]))
	}
	return out
}

func (d *providersDialog) addFormLines() []string {
	lines := make([]string, 0, len(d.addEds)+3)
	title := "add custom provider (openai-compatible)"
	if d.editOrig != "" {
		title = "edit custom provider (openai-compatible)"
	}
	lines = append(lines, palette.Assistant.On(title), "")
	for i := range d.addEds {
		label := pad(providerAddLabels[i], 15)
		val := d.addEds[i].text()
		if i == d.addIdx {
			val = string(d.addEds[i].value[:d.addEds[i].cursor]) +
				palette.Primary.On("▏") + string(d.addEds[i].value[d.addEds[i].cursor:])
			label = palette.Primary.On(label)
		} else {
			label = palette.Subtle.On(label)
		}
		lines = append(lines, label+val)
	}
	lines = append(lines, "",
		palette.Subtle.On("  reasoning: inline (default) · field (thinking models: Moonshot/Kimi) · strip"),
		palette.Subtle.On("  context window: tokens, blank = auto (Kimi K2.x 262144 · moonshot-v1-128k 131072)"),
		palette.Subtle.On("  price /M: USD per 1M tokens as in/out, blank = unmetered (e.g. 0.6/2.5)"))
	return lines
}

// draw renders the panel as a standalone centered dialog (used when pushed
// directly, e.g. in tests). Inline use goes through detailLines + the host's
// footer; standalone re-appends the footer hint so the box reads on its own.
func (d *providersDialog) draw(scr uv.Screen, area uv.Rectangle) {
	lines := append(d.detailLines(area.Dx()), "", d.footerHint())
	drawDialogBox(scr, area, "providers", lines)
}

// tags renders the per-provider status badges (active / origin / credential)
// from the shared word-badge vocabulary.
func (d *providersDialog) tags(def backends.ProviderDefinition) string {
	return providerBadges(
		def.Name == d.active,
		!def.Builtin,
		isOAuthProvider(def.Name),
		d.hasKey[def.Name],
		def.UsesAPIKey(),
	)
}
