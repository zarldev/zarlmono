package tui

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

// RenderCockpitLines renders the cockpit body at the requested content width.
func (m *UI) RenderCockpitLines(width int) []string {
	return m.cockpitLines(width)
}

// SetPlanMode selects the durable workflow mode shown by the UI.
func (m *UI) SetPlanMode(enabled bool) {
	m.session.PlanMode = enabled
}

// SetPlan replaces the structured plan rendered by the planning pane.
func (m *UI) SetPlan(plan code.Plan) { m.session.Plan = plan }

// Submit sends text through the same queue-or-run path as the composer.
func (m *UI) Submit(text string) tea.Cmd { return m.submit(text) }

// SetRunning controls whether user input is queued for the active run.
func (m *UI) SetRunning(running bool) { m.session.Run.Running = running }

// SetStartupCommand replaces the required startup metadata command.
func (m *UI) SetStartupCommand(cmd tea.Cmd) { m.startupCmd = cmd }

// SetStartupReady controls whether the first prompt may start immediately.
func (m *UI) SetStartupReady(ready bool) { m.startupReady = ready }

// StartupPrompt returns the prompt waiting for required startup work.
func (m *UI) StartupPrompt() string { return m.startupPrompt }

// ApplyLimits refreshes the live runner limits from persisted settings.
func (m *UI) ApplyLimits() { m.applyLimits() }

// RepointProvider applies a resolved provider target to the live session.
func (m *UI) RepointProvider(prov llm.Provider, spec engine.ProviderSpec, window int, err error) bool {
	return m.handleRepointMsg(providerRepointedMsg{prov: prov, spec: spec, window: window, err: err})
}

// ActiveProviderSpec returns the provider target currently shown by the session.
func (m *UI) ActiveProviderSpec() engine.ProviderSpec { return m.session.ActiveProviderSpec() }

// RestoreMessages rebuilds the visible transcript from persisted messages.
func (m *UI) RestoreMessages(messages []llm.Message) { m.timeline.restoreMessages(messages) }

// DecodeSessionHistory validates persisted conversation history while tolerating corrupt auxiliary blobs.
func DecodeSessionHistory(rec db.SessionRecord) ([]llm.Message, error) {
	saved, err := decodeSavedSession(rec)
	if err != nil {
		return nil, err
	}
	return saved.History, nil
}

// SetToast records an informational status notification.
func (m *UI) SetToast(msg string) { m.session.SetToast(msg) }

// SetSuccessToast records a successful status notification.
func (m *UI) SetSuccessToast(msg string) { m.session.SetSuccessToast(msg) }

// SetErrorToast records a failed status notification.
func (m *UI) SetErrorToast(msg string) { m.session.SetErrorToast(msg) }

// SetSessionIdentity records the persisted session identity used by session utilities.
func (m *UI) SetSessionIdentity(id, label string, manual bool, startedAt time.Time) {
	m.session.SetIdentity(id, label, manual, startedAt)
}

// OpenToolHistory opens the session's persisted tool-output viewer.
func (m *UI) OpenToolHistory(store *db.Store, sessionID string) {
	m.settings = &engine.Settings{Store: store}
	m.session.ID = sessionID
	m.overlay.push(newToolHistory(m.appContext(), store, sessionID))
}

// ToolHistorySelection returns the selected tool call ID and its zero-based index.
func (m *UI) ToolHistorySelection() (string, int) {
	h, ok := m.overlay.top().(*toolHistory)
	if !ok || len(h.summaries) == 0 {
		return "", 0
	}
	return h.summaries[h.cursor].ToolCallID, h.cursor
}

// SessionIdentity returns the current persisted session identity.
func (m *UI) SessionIdentity() string { return m.session.ID }

// RenderSparkline renders the cockpit's uncoloured single-row sparkline.
func RenderSparkline(vals []float64, width int, normMax float64) string {
	return sparkline(vals, width, normMax, "", "", nil)
}

// RenderStackedBar renders an uncoloured proportional bar from matching weights and glyphs.
func RenderStackedBar(weights []float64, glyphs []rune, width int) string {
	segs := make([]barSeg, 0, min(len(weights), len(glyphs)))
	for i := 0; i < len(weights) && i < len(glyphs); i++ {
		segs = append(segs, barSeg{weight: weights[i], glyph: glyphs[i]})
	}
	return stackedBar(segs, width)
}

// MarkCockpitThreshold replaces the visible bar cell at col with the pressure marker.
func MarkCockpitThreshold(bar string, col int) string { return markThreshold(bar, col) }

// FormatCockpitCount formats a compact cockpit count.
func FormatCockpitCount(n int) string { return fmtCount(n) }

// FormatCockpitDuration formats a compact cockpit duration.
func FormatCockpitDuration(d time.Duration) string { return fmtDuration(d) }

// FormatCockpitUSD formats a compact cockpit dollar amount.
func FormatCockpitUSD(v float64) string { return fmtUSD(v) }

// RenderUtilitySurface renders the shared open master/detail utility chrome.
func RenderUtilitySurface(width, height, navWidth int, title string, tabs []string, active int, summary string) string {
	buf := uv.NewScreenBuffer(width, height)
	layout, ok := drawUtilitySplitPane(buf, buf.Bounds(), navWidth)
	if !ok {
		return ""
	}
	drawOverlayContext(buf, layout, overlayTopBar(title, tabs, active, summary, layout.Context.Dx()), palette.Border)
	return buf.Render()
}

// RenderFooterToast renders a themed status notification for the requested semantic tone.
func RenderFooterToast(text, tone string) string {
	var t toastTone
	switch strings.ToLower(tone) {
	case "success":
		t = toastSuccess
	case "error":
		t = toastError
	case "warning":
		t = toastWarn
	}
	return renderFooterToast(text, t)
}

// RenderTypedToolResult renders a structured tool result using the transcript's typed-result presentation.
func RenderTypedToolResult(width int, toolName, fallbackText string, data any) []string {
	return renderTypedToolResult(width, contentBlock{kind: contentToolResult, toolName: toolName, text: fallbackText, data: data})
}

// StatusLooksLikeError reports whether status text receives the error toast treatment.
func StatusLooksLikeError(status string) bool { return isErrorStatus(status) }

// RuneWidth returns the width used by the uncoloured cockpit primitive tests.
func RuneWidth(s string) int { return utf8.RuneCountInString(s) }

// GuardrailSummary renders the configured guardrail summary shown by the inspector.
func GuardrailSummary(deps guardrails.Deps) string { return guardrailSummary(deps) }

// ListWindow computes the visible list range and overflow indicators.
func ListWindow(cursor, n, rows int) (int, int, bool, bool) {
	return listWindow(cursor, n, rows)
}

// WindowAroundCursor computes a cursor-containing list range.
func WindowAroundCursor(cursor, n, rows int) (int, int) {
	return windowAroundCursor(cursor, n, rows)
}

// ResolveMCPAuthToken resolves and migrates an MCP server authentication token.
func ResolveMCPAuthToken(ctx context.Context, settings *engine.Settings, row db.MCPServerRow) string {
	return resolveMCPAuthToken(ctx, settings, row)
}

// MCPAuthKeyProvider returns the vault provider key used for an MCP server token.
func MCPAuthKeyProvider(name string) string { return mcpAuthKeyProvider(name) }

// FormatRateLimit formats a rate-limit error for display.
func FormatRateLimit(err *llm.RateLimitError) string { return formatRateLimit(err) }

// ContentKind selects a transcript content presentation.
type ContentKind uint8

const (
	ContentPlain ContentKind = iota
	ContentMarkdown
	ContentCode
	ContentDiff
	ContentToolResult
)

// ContentTone selects a transcript content tone.
type ContentTone uint8

const (
	ContentToneDefault ContentTone = iota
	ContentToneMuted
)

// Content describes a renderable transcript block.
type Content struct {
	Kind               ContentKind
	Text               string
	ToolName           string
	Hint               string
	Syntax             string
	Rail               string
	BodyPrefix         string
	FirstPrefix        string
	ContinuationPrefix string
	CacheKey           string
	MaxLines           int
	LineNumbers        bool
	StripANSI          bool
	Tone               ContentTone
}

// RenderContent renders a transcript content block.
func RenderContent(width int, c Content) []string {
	kinds := [...]contentKind{contentPlain, contentMarkdown, contentCode, contentDiff, contentToolResult}
	kind := contentPlain
	if int(c.Kind) < len(kinds) {
		kind = kinds[c.Kind]
	}
	return renderContentBlock(width, contentBlock{
		kind: kind, text: c.Text, toolName: c.ToolName, hint: c.Hint, syntax: c.Syntax,
		rail: c.Rail, bodyPrefix: c.BodyPrefix, firstPrefix: c.FirstPrefix,
		continuationPrefix: c.ContinuationPrefix, cacheKey: c.CacheKey,
		maxLines: c.MaxLines, lineNumbers: c.LineNumbers, stripANSI: c.StripANSI,
		tone: contentTone(c.Tone),
	})
}

// FindSafeMarkdownBoundary returns the largest prefix safe to render while streaming.
func FindSafeMarkdownBoundary(content string) int { return findSafeMarkdownBoundary(content) }

// StreamingMarkdown incrementally renders markdown while retaining stable prefixes.
type StreamingMarkdown struct{ state streamingMarkdown }

// Render renders the current complete streaming prefix.
func (s *StreamingMarkdown) Render(content string, width int) string {
	return s.state.render(content, width)
}

// StablePrefix returns the prefix retained across incremental renders.
func (s *StreamingMarkdown) StablePrefix() string { return s.state.stablePrefix }

// ToolArgHint returns the compact transcript hint for a tool invocation.
func ToolArgHint(name string, params map[string]any) string { return toolArgHint(name, params) }

// RenderThinking renders a completed, expanded reasoning block.
func RenderThinking(width int, text string) []string {
	return (&thinkingItem{text: text, expanded: true, done: true}).render(width)
}

// OpenThemePicker opens the live-preview theme switcher.
func (m *UI) OpenThemePicker() {
	m.overlay.push(newThemePicker())
}

// ComposerText returns the editor's current source text.
func (m *UI) ComposerText() string { return m.composer.text() }

// OpenIntro opens the fresh-session prompt surface for workspace.
func (m *UI) OpenIntro(workspace string) { m.intro = newIntroPane(workspace, nil, "", "") }

// IntroPrompt returns the fresh-session prompt's current source text.
func (m *UI) IntroPrompt() string {
	if m.intro == nil {
		return ""
	}
	return string(m.intro.prompt)
}

// SetConfirmQuit controls whether the quit shortcut opens a confirmation dialog.
func (m *UI) SetConfirmQuit(enabled bool) { m.session.ConfirmQuit = enabled }

// RenderPRLine renders pull-request state for the sidebar.
func RenderPRLine(pr *PRInfo) string { return prLine(pr) }

// AddTranscriptUser appends a user-authored message to the visible transcript.
func (m *UI) AddTranscriptUser(text string) { m.timeline.addUser(text) }

// RenderTranscript renders the transcript viewport at the requested dimensions.
func (m *UI) RenderTranscript(width, height int) []string {
	m.timeline.viewWidth, m.timeline.viewHeight = width, height
	return m.timeline.renderViewport(width, height)
}

// OpenDiffBrowser opens the session working-set diff browser.
func (m *UI) OpenDiffBrowser(ws *WorkingSet) {
	m.overlay.push(newDiffBrowser(ws))
}
