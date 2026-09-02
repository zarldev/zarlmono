package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
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

// AttachFile attaches a workspace text file through the production path.
func (m *UI) AttachFile(path string) error { return m.attachFilePath(path) }

// AttachImage attaches an image through the production path.
func (m *UI) AttachImage(path string) error { return m.attachImagePath(path) }

// StartSubmittedTurn applies the top-level start event that durably records input.
func (m *UI) StartSubmittedTurn(taskID, prompt string) {
	effect := m.session.applyConversationStarted(teasink.ConversationStartedMsg{
		TaskID: taskID, Prompt: prompt,
	}, time.Now())
	m.timeline.addUserWithAttachments(effect.PromptToRender, effect.Attachments)
}

// SetRunning controls whether user input is queued for the active run.
func (m *UI) SetRunning(running bool) { m.session.Run.Running = running }

// SetStartupCommand replaces the required startup metadata command.
func (m *UI) SetStartupCommand(cmd tea.Cmd) { m.startupCmd = cmd }

// SetStartupReady controls whether the first prompt may start immediately.
func (m *UI) SetStartupReady(ready bool) { m.startupReady = ready }

// StartupPrompt returns the prompt waiting for required startup work.
func (m *UI) StartupPrompt() string { return m.startupPrompt }

// ApplyStartupReady applies the production startup-ready transition.
func (m *UI) ApplyStartupReady() tea.Cmd {
	_, cmd := m.Update(startupReadyMsg{})
	return cmd
}

// ApplyLimits refreshes the live runner limits from persisted settings.
func (m *UI) ApplyLimits() { m.applyLimits() }

// RepointProvider applies a resolved provider target to the live session.
func (m *UI) RepointProvider(prov llm.Provider, spec engine.ProviderSpec, window int, err error) bool {
	return m.handleRepointMsg(providerRepointedMsg{prov: prov, spec: spec, window: window, err: err})
}

// ResumeSavedSession loads one persisted session through the production resume path.
func (m *UI) ResumeSavedSession(ctx context.Context, id string) error {
	saved, err := loadSavedSession(ctx, m.settings.Store, id)
	if err != nil {
		return err
	}
	m.completeResumeSession(saved, false)
	return nil
}

// ActiveProviderSpec returns the provider target currently shown by the session.
func (m *UI) ActiveProviderSpec() engine.ProviderSpec { return m.session.ActiveProviderSpec() }

// AddTranscriptMessages appends semantic messages through the live transcript path.
func (m *UI) AddTranscriptMessages(messages []llm.Message) {
	for i, message := range messages {
		switch message.Role {
		case llm.RoleUser:
			m.timeline.addUser(message.Content)
		case llm.RoleAssistant:
			taskID := fmt.Sprintf("test-transcript-%d", i)
			m.timeline.startTurn(taskID, 0)
			m.timeline.appendContent(taskID, 0, message.Content)
			m.timeline.endTurn(taskID)
		}
	}
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

// ForceTranscriptPersist snapshots the canonical thread into the serialized persistence queue.
func (m *UI) ForceTranscriptPersist() tea.Cmd { return m.persistTranscriptNow() }

// ScheduleTranscriptPersist schedules a debounced canonical-thread write.
func (m *UI) ScheduleTranscriptPersist() tea.Cmd { return m.scheduleTranscriptPersist() }

// AddPartialTranscript appends an unfinished turn without scheduling persistence.
func (m *UI) AddPartialTranscript(taskID, prompt, content string) {
	m.timeline.addUser(prompt)
	m.timeline.startTurn(taskID, 0)
	m.timeline.appendContent(taskID, 0, content)
}

// HandleTranscriptDebounce applies a transcript debounce message for generation.
func (m *UI) HandleTranscriptDebounce(generation uint64) tea.Cmd {
	cmd, _ := m.handleDraftPersistenceMsg(transcriptDebounceMsg{Generation: generation})
	return cmd
}

// TranscriptGeneration returns the current stream persistence generation.
func (m *UI) TranscriptGeneration() uint64 { return m.transcriptGeneration }

// QueueTranscriptPersist snapshots the canonical thread without starting the command.
func (m *UI) QueueTranscriptPersist() {
	m.sessionPersistRunning = true
	m.persistTranscriptNow()
	m.sessionPersistRunning = false
}

// SessionPersistQueueKinds returns the current queued operation kinds for tests.
func (m *UI) SessionPersistQueueKinds() []string {
	kinds := make([]string, len(m.sessionPersistQueue))
	for i, op := range m.sessionPersistQueue {
		switch op.kind {
		case sessionPersistDraft:
			kinds[i] = "draft"
		case sessionPersistClearDraft:
			kinds[i] = "clear-draft"
		case sessionPersistTranscript:
			kinds[i] = "transcript"
		case sessionPersistFull:
			kinds[i] = "commit"
		case sessionPersistDelete:
			kinds[i] = "delete"
		}
	}
	return kinds
}

// QueueLatestTranscriptPersist queues the current transcript without starting it.
func (m *UI) QueueLatestTranscriptPersist() {
	m.sessionPersistRunning = true
	m.persistTranscriptNow()
	m.sessionPersistRunning = false
}

// QueueDeletePersist queues a delete barrier without starting it.
func (m *UI) QueueDeletePersist(sessionID string) {
	m.sessionPersistRunning = true
	m.enqueueSessionPersist(sessionPersistOp{kind: sessionPersistDelete, oldID: sessionID})
	m.sessionPersistRunning = false
}

// StartTranscriptPersistWithoutDelivering starts one transcript command and
// discards its Bubble Tea message after the store write completes.
func (m *UI) StartTranscriptPersistWithoutDelivering() tea.Cmd {
	cmd := m.persistTranscriptNow()
	return func() tea.Msg {
		cmd()
		return nil
	}
}

// StartBlockingTranscriptPersist starts a transcript write through a test gate.
func (m *UI) StartBlockingTranscriptPersist(started chan<- struct{}, release <-chan struct{}, result error) tea.Cmd {
	op := sessionPersistOp{kind: sessionPersistTranscript, generation: m.transcriptGeneration, done: make(chan sessionPersistedMsg, 1)}
	m.sessionPersistCurrent = &op
	m.sessionPersistRunning = true
	return func() tea.Msg {
		close(started)
		<-release
		msg := sessionPersistedMsg{kind: op.kind, generation: op.generation, err: result}
		op.done <- msg
		close(op.done)
		return msg
	}
}

// QueueCommitBarrier queues a terminal barrier for the current transcript generation.
func (m *UI) QueueCommitBarrier() {
	m.sessionPersistRunning = true
	m.enqueueSessionPersist(sessionPersistOp{
		kind: sessionPersistFull, generation: m.transcriptGeneration,
		snapshot: &sessionSnapshot{record: db.SessionRecord{ID: m.session.ID}},
	})
	m.sessionPersistRunning = false
}

// QueueTranscriptGeneration queues a synthetic transcript generation for barrier tests.
func (m *UI) QueueTranscriptGeneration(generation uint64) {
	m.sessionPersistRunning = true
	m.enqueueSessionPersist(sessionPersistOp{
		kind: sessionPersistTranscript, generation: generation,
		transcript: &transcriptSnapshot{update: db.TranscriptUpdate{SessionID: m.session.ID}},
	})
	m.sessionPersistRunning = false
}

// TranscriptText returns persisted entry payloads as text for black-box assertions.
func TranscriptText(saved db.SessionTranscript) string {
	var out strings.Builder
	for _, entry := range saved.Entries {
		out.Write(entry.PayloadJSON)
		out.WriteByte('\n')
	}
	return out.String()
}

// CanonicalThread returns the canonical thread for behavior tests.
func (m *UI) CanonicalThread() transcript.Thread { return m.timeline.thread.Thread() }

// PersistedTranscriptRevision returns the last acknowledged thread revision.
func (m *UI) PersistedTranscriptRevision() uint64 { return m.transcriptPersisted }

// ReplayTranscriptEvents reduces semantic transcript events into the live projection.
func (m *UI) ReplayTranscriptEvents(events ...any) {
	for _, event := range events {
		switch event := event.(type) {
		case transcript.UserSubmitted:
			m.timeline.addUser(event.Text)
		case transcript.TurnStarted:
			m.timeline.startTurn(event.TurnID, 0)
		case transcript.AssistantDelta:
			m.timeline.appendContent(event.TurnID, 0, event.Delta)
		case transcript.ReasoningDelta:
			m.timeline.appendThinking(event.TurnID, 0, event.Delta)
		case transcript.ToolStarted:
			m.timeline.startToolWithParent(event.TurnID, 0, event.ToolID, event.Name, event.Argument, event.ParentToolID, event.Sequence)
		case transcript.ToolFinished:
			m.timeline.finishTool(event.ToolID, "", nil, time.Duration(event.DurationMS)*time.Millisecond, event.Failed, tools.Kinds.UNKNOWN, event.Effect)
		case transcript.DiffAdded:
			m.timeline.addDiff(event.TurnID, event.Path, event.Diff)
		case transcript.PlanUpdated:
			m.timeline.addPlanUpdate(event.TurnID, event.Plan)
		case transcript.TurnFinished:
			m.timeline.endTurn(event.TurnID)
		default:
			panic("unsupported test transcript event")
		}
	}
}

// RestoreCanonicalTranscript projects a canonical thread into a fresh timeline.
func (m *UI) RestoreCanonicalTranscript(thread transcript.Thread) { m.timeline.restoreThread(thread) }

// PendingTranscriptPersistence returns the strongest undrained reducer policy.
func (m *UI) PendingTranscriptPersistence() string {
	return m.timeline.pendingPersist.String()
}

// DrainTranscriptPersistence consumes the current reducer persistence policy.
func (m *UI) DrainTranscriptPersistence() tea.Cmd { return m.transcriptPersistenceCmd() }
