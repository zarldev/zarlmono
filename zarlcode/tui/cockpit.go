package tui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// cacheReadRate is the fraction of the normal input price that a
// prompt-cache *read* costs. Anthropic and OpenAI both bill cache hits at
// roughly a tenth of fresh input, so a cached token is ~90% saved. Used by
// both the per-turn cost and the "cache saved" figure; kept conservative so
// the savings number never overstates.
const cacheReadRate = 0.10

// historyCap bounds the per-turn sample ring. ~120 turns is far more than a
// sparkline ever shows; the cap just stops an all-day session from growing
// the slice without bound.
const historyCap = 120

// turnSample is one point in the cockpit's per-turn time series — enough to
// drive the fill / throughput / cache / cost sparklines without retaining
// the whole turn. compacted flags the point where a compaction trimmed the
// window so the fill sparkline can paint the sawtooth dip in Warning.
type turnSample struct {
	fillFrac  float64
	tokIn     int
	tokOut    int
	cached    int
	tokPerSec float64
	costUSD   float64
	compacted bool
}

// toolStat is the session-cumulative footprint of one tool: how often it
// was called, how many failed, and the total wall-clock spent in it.
type toolStat struct {
	calls int
	fails int
	dur   time.Duration
}

// RunState is the cockpit model: the live summary of the current top-level
// run plus the session-cumulative accounting (tokens, cost, tools,
// compaction, per-turn history) the sidebar and dashboard render. It is
// folded from runner events in handleRunnerMsg — every field here is set
// from a teasink message, never derived at the call site.
type RunState struct {
	// --- live run (reset each top-level turn) ---
	Running      bool
	iterations   int
	tools        int // completed tool calls this turn
	toolsRunning int
	maxDepth     int // deepest active sub-agent this turn

	// turnStartedAt / turnCompletionTokens drive the live tok/s shown on the
	// timeline title while a turn streams. turnOutBytes remains a visible-output
	// counter for future UI use, but not for tok/s.
	turnStartedAt        time.Time
	turnOutBytes         int
	turnCompletionTokens int

	// liveCtx is the current context occupancy (prompt tokens of the most
	// recent iteration) — the gauge numerator. liveTotal is the running
	// total tokens. Both update mid-turn so the gauge climbs live.
	liveCtx   int
	liveTotal int

	// Per-role context composition from the latest iteration's
	// ContextBreakdown (raw bytes + message counts). Drives the role-
	// partitioned context graph; all zero until the first breakdown lands,
	// where the graph falls back to the cached/fresh/free split.
	ctxSysBytes, ctxUserBytes, ctxAsstBytes, ctxToolBytes int
	ctxSkillBytes, ctxAgentBytes                          int
	ctxSysMsgs, ctxUserMsgs, ctxAsstMsgs, ctxToolMsgs     int

	// --- identity / config (set once at wiring) ---
	window       int     // context window in tokens (gauge denominator)
	local        bool    // local/unmetered backend (llamacpp/ollama)
	subscription bool    // flat-subscription backend (codex/claude-code)
	inCostPer1k  float64 // USD per 1k input tokens; 0 == unknown
	outCostPer1k float64 // USD per 1k output tokens

	// pressureWindow / pressureReserve define the compaction pressure
	// threshold (window - reserve). Zero pressureWindow means no
	// threshold is configured — the pressure marker and hints hide.
	pressureWindow  int
	pressureReserve int

	// --- last completed turn ---
	lastIn, lastOut, lastTotal, lastCached int
	lastDuration                           time.Duration
	lastIter                               int
	lastTurnAt                             time.Time

	// --- session rollup (since reset / launch) ---
	startedAt           time.Time
	sessionTurns        int
	sessionIn           int
	sessionOut          int
	sessionCached       int
	sessionInParent     int // parent-only prompt tokens (excludes sub-agent delegation)
	sessionOutParent    int // parent-only completion tokens
	sessionCachedParent int
	sessionToolCalls    int

	// --- per-tool cumulative stats ---
	toolStats map[string]toolStat

	// --- last compaction ---
	compactBefore int
	compactAfter  int
	compactBytes  int // raw bytes the engine trimmed (BytesTrimmed)
	compactEngine string
	compactAt     time.Time
	compactions   int

	// --- per-turn history (ring; newest last) ---
	history        []turnSample
	pendingCompact bool // flag the next recorded sample as a compaction dip
}

// SessionUsageSnapshot is the persistable rollup of a session's token
// usage and turn/tool counts, stored in last_usage_json so the cockpit's
// session totals and cost survive -continue. The live-run counters,
// per-turn history ring, and pricing are not persisted — they reset or
// re-derive on the next turn.
type SessionUsageSnapshot struct {
	Turns        int `json:"turns"`
	ToolCalls    int `json:"tool_calls"`
	In           int `json:"in"`
	Out          int `json:"out"`
	Cached       int `json:"cached"`
	InParent     int `json:"in_parent"`
	OutParent    int `json:"out_parent"`
	CachedParent int `json:"cached_parent"`
}

// UsageSnapshot captures the session rollup for persistence.
func (s *RunState) UsageSnapshot() SessionUsageSnapshot {
	return SessionUsageSnapshot{
		Turns:        s.sessionTurns,
		ToolCalls:    s.sessionToolCalls,
		In:           s.sessionIn,
		Out:          s.sessionOut,
		Cached:       s.sessionCached,
		InParent:     s.sessionInParent,
		OutParent:    s.sessionOutParent,
		CachedParent: s.sessionCachedParent,
	}
}

// RestoreUsage seeds the session rollup from a persisted snapshot so a
// resumed session's cockpit reflects the tokens it already spent. Live
// counters are untouched; the next turn accumulates on top.
func (s *RunState) RestoreUsage(snap SessionUsageSnapshot) {
	s.sessionTurns = snap.Turns
	s.sessionToolCalls = snap.ToolCalls
	s.sessionIn = snap.In
	s.sessionOut = snap.Out
	s.sessionCached = snap.Cached
	s.sessionInParent = snap.InParent
	s.sessionOutParent = snap.OutParent
	s.sessionCachedParent = snap.CachedParent
}

// reset clears the live-run counters at the start of a new top-level turn
// while preserving session rollup, history, pricing, and window — those
// accumulate across turns until the program restarts (or a future /clear).
func (s *RunState) reset() {
	s.Running = false
	s.iterations = 0
	s.tools = 0
	s.toolsRunning = 0
	s.maxDepth = 0
	s.turnStartedAt = time.Now()
	s.turnOutBytes = 0
	s.turnCompletionTokens = 0
	if s.startedAt.IsZero() {
		s.startedAt = time.Now()
	}
}

// liveTokPerSec is the run-title throughput. Prefer provider-reported
// completion tokens from completed iterations in the current turn; that is the
// same token basis the cockpit's LAST TURN row uses. Before the current turn has
// any provider usage, fall back to the last completed turn rather than showing a
// low visible-bytes estimate that disagrees with the cockpit.
func (s *RunState) liveTokPerSec() float64 {
	if s.Running && !s.turnStartedAt.IsZero() {
		if el := time.Since(s.turnStartedAt).Seconds(); el >= 0.5 && s.turnCompletionTokens > 0 {
			return float64(s.turnCompletionTokens) / el
		}
	}
	return s.tokPerSec()
}

// foldIteration folds an iteration-completed event: bumps the iteration
// count and updates the live context estimate so the gauge tracks the
// growing prompt mid-turn. Gauges snapshot from u (occupancy — the most
// recent usage observed across the Run); flow figures (lastOut, the
// tok/s accumulator) come from delta (this iteration's own usage) so a
// terminal iteration whose provider dropped usage can't re-count the
// previous iteration's output. Snapshots survive foldTurnComplete —
// which receives the cumulative totalUsage — without being overwritten
// by the multi-iteration sum.
func (s *RunState) foldIteration(u, delta *llm.Usage) {
	s.iterations++
	if u != nil {
		if u.PromptTokens > 0 {
			s.liveCtx = u.PromptTokens
			s.lastIn = u.PromptTokens
		}
		if u.TotalTokens > 0 {
			s.liveTotal = u.TotalTokens
			s.lastTotal = u.TotalTokens
		}
		if u.CachedTokens > 0 {
			s.lastCached = u.CachedTokens
		}
	}
	if delta != nil && delta.CompletionTokens > 0 {
		s.lastOut = delta.CompletionTokens
		s.turnCompletionTokens += delta.CompletionTokens
	}
}

// setContextBreakdown stores the latest per-role composition so the context
// graph can partition the bar by role. Called on the top-level Run only.
func (s *RunState) setContextBreakdown(b *runner.ContextBreakdown) {
	if b == nil {
		return
	}
	s.ctxSysBytes, s.ctxUserBytes = b.SystemBytes, b.UserBytes
	s.ctxAsstBytes, s.ctxToolBytes = b.AssistantBytes, b.ToolBytes
	s.ctxSkillBytes, s.ctxAgentBytes = b.SkillBytes, b.AgentBytes
	s.ctxSysMsgs, s.ctxUserMsgs = b.SystemMsgs, b.UserMsgs
	s.ctxAsstMsgs, s.ctxToolMsgs = b.AssistantMsgs, b.ToolMsgs
}

// hasBreakdown reports whether a per-role composition is available (drives
// the choice between the role graph and the cached/fresh/free fallback).
func (s *RunState) hasBreakdown() bool {
	return s.ctxSysBytes+s.ctxUserBytes+s.ctxAsstBytes+s.ctxToolBytes > 0
}

// foldTool records a tool call completing (success or failure): decrements
// the in-flight counter, bumps the per-turn and session counts, and
// accumulates the tool's cumulative footprint.
func (s *RunState) foldTool(name string, dur time.Duration, failed bool) {
	if s.toolsRunning > 0 {
		s.toolsRunning--
	}
	s.tools++
	s.sessionToolCalls++
	if name == "" {
		name = "tool"
	}
	if s.toolStats == nil {
		s.toolStats = map[string]toolStat{}
	}
	st := s.toolStats[name]
	st.calls++
	st.dur += dur
	if failed {
		st.fails++
	}
	s.toolStats[name] = st
}

// foldTurnComplete folds a top-level conversation-completed event: records
// the last-turn token flow, rolls it into the session totals, finalises the
// live context estimate, and pushes a history sample.
//
// The Usage on ConversationEnded is the cumulative totalUsage across all
// iterations of the Run — correct for session accumulation, but NOT for the
// per-turn gauges (lastIn, lastOut, liveCtx). Those are snapshotted by
// foldIteration from the per-iteration IterationCompleted event. We only
// accumulate session totals here; we never overwrite the per-iteration fields.
func (s *RunState) foldTurnComplete(u *llm.Usage, dur time.Duration, iters int) {
	s.Running = false
	s.lastDuration = dur
	s.lastTurnAt = time.Now()
	if iters > 0 {
		s.lastIter = iters
	} else {
		s.lastIter = s.iterations
	}
	if u != nil {
		// Session totals use the cumulative TotalUsage — added once per turn,
		// this correctly reflects the full spend of the Run (including every
		// iteration's tokens).
		s.sessionIn += u.PromptTokens
		s.sessionOut += u.CompletionTokens
		s.sessionCached += u.CachedTokens
		s.sessionInParent += u.PromptTokens
		s.sessionOutParent += u.CompletionTokens
		s.sessionCachedParent += u.CachedTokens
	}
	s.sessionTurns++
	s.recordSample()
}

// foldSubAgentUsage rolls a completed sub-agent Run's token spend into the
// session totals so cost reflects delegated work, without disturbing the
// top-level context gauge or last-turn figures.
func (s *RunState) foldSubAgentUsage(u *llm.Usage) {
	if u == nil {
		return
	}
	s.sessionIn += u.PromptTokens
	s.sessionOut += u.CompletionTokens
	s.sessionCached += u.CachedTokens
}

// foldCompaction folds a compaction-applied event: stamps the last-compaction
// detail, drops the live context estimate by the reclaimed amount so the
// gauge falls immediately, and arms pendingCompact so the next history
// sample reads as a sawtooth dip. bytes is the engine's raw BytesTrimmed; the
// context gauge falls by its token-equivalent (bytes/4).
func (s *RunState) foldCompaction(before, after, bytes int, engine string) {
	reclaimedTok := bytes / 4
	s.compactBefore = before
	s.compactAfter = after
	s.compactBytes = bytes
	s.compactEngine = engine
	s.compactAt = time.Now()
	s.compactions++
	s.pendingCompact = true
	if reclaimedTok > 0 {
		if s.liveCtx -= reclaimedTok; s.liveCtx < 0 {
			s.liveCtx = 0
		}
	}
}

// recordSample appends the current turn to the history ring.
func (s *RunState) recordSample() {
	var tps float64
	if s.lastDuration > 0 && s.lastOut > 0 {
		tps = float64(s.lastOut) / s.lastDuration.Seconds()
	}
	s.history = append(s.history, turnSample{
		fillFrac:  s.fillFrac(),
		tokIn:     s.lastIn,
		tokOut:    s.lastOut,
		cached:    s.lastCached,
		tokPerSec: tps,
		costUSD:   s.turnCost(),
		compacted: s.pendingCompact,
	})
	s.pendingCompact = false
	if len(s.history) > historyCap {
		s.history = s.history[len(s.history)-historyCap:]
	}
}

// fillFrac is the live context occupancy as a fraction of the window.
func (s *RunState) fillFrac() float64 {
	if s.window <= 0 {
		return 0
	}
	return float64(s.effectiveUsed()) / float64(s.window)
}

// effectiveUsed is the context occupancy the gauge and bars render: the
// provider-reported prompt tokens when available, else a chars/4 estimate
// from the role breakdown. llama.cpp's OpenAI-compat endpoint sometimes
// omits usage, leaving liveCtx at 0 even though the window is full of real
// content — falling back to the breakdown keeps the gauge honest instead of
// reading 0%. Zero only when there's genuinely nothing in the window yet.
func (s *RunState) effectiveUsed() int {
	if s.liveCtx > 0 {
		return s.liveCtx
	}
	return (s.ctxSysBytes + s.ctxUserBytes + s.ctxAsstBytes + s.ctxToolBytes) / 4
}

// pressureThresholdCol returns the column position (0-indexed, within a bar
// of barWidth cells) where the pressure threshold marker should sit, or -1
// when no threshold is configured or the bar can't place it.
func (s *RunState) pressureThresholdCol(barWidth int) int {
	if s.pressureWindow <= 0 || s.pressureReserve < 0 || barWidth <= 0 {
		return -1
	}
	threshold := s.pressureWindow - s.pressureReserve
	if threshold <= 0 || threshold >= s.pressureWindow {
		return -1
	}
	col := threshold * barWidth / s.pressureWindow
	if col >= barWidth {
		return barWidth - 1
	}
	return col
}

// markThresholdBar overlays the pressure threshold marker (┤) onto bar
// when a threshold is configured and the column falls within the bar.
func (s *RunState) markThresholdBar(bar string, barWidth int) string {
	col := s.pressureThresholdCol(barWidth)
	if col < 0 {
		return bar
	}
	return markThreshold(bar, col)
}

// tokPerSec is the last turn's output throughput (completion tokens / s).
func (s *RunState) tokPerSec() float64 {
	if s.lastDuration <= 0 || s.lastOut <= 0 {
		return 0
	}
	return float64(s.lastOut) / s.lastDuration.Seconds()
}

// hasPricing reports whether cost figures are meaningful (a hosted model
// with known per-token pricing). Local models price at zero, so the cockpit
// renders "local — no metered cost" instead of a row of $0.00.
func (s *RunState) hasPricing() bool {
	return s.inCostPer1k > 0 || s.outCostPer1k > 0
}

// cost prices an (input, cached, output) token triple, charging cached
// input at cacheReadRate of the normal input price.
func (s *RunState) cost(in, cached, out int) float64 {
	fresh := in - cached
	if fresh < 0 {
		fresh = 0
	}
	return float64(fresh)/1000*s.inCostPer1k +
		float64(cached)/1000*s.inCostPer1k*cacheReadRate +
		float64(out)/1000*s.outCostPer1k
}

// turnCost is the USD cost of the last completed turn.
func (s *RunState) turnCost() float64 {
	return s.cost(s.lastIn, s.lastCached, s.lastOut)
}

// sessionCost is the cumulative USD cost this session.
func (s *RunState) sessionCost() float64 {
	return s.cost(s.sessionIn, s.sessionCached, s.sessionOut)
}

// sessionCostParent is the cumulative USD cost of top-level turns only
// (excludes sub-agent delegation).
func (s *RunState) sessionCostParent() float64 {
	return s.cost(s.sessionInParent, s.sessionCachedParent, s.sessionOutParent)
}

// hasSubAgentUsage reports whether sub-agent delegation contributed to
// this session's totals.
func (s *RunState) hasSubAgentUsage() bool {
	return s.sessionIn > s.sessionInParent || s.sessionOut > s.sessionOutParent
}

// cacheSaved is the USD not paid this session because input tokens were
// served from the prompt cache (the discount on cached input).
func (s *RunState) cacheSaved() float64 {
	return float64(s.sessionCached) / 1000 * s.inCostPer1k * (1 - cacheReadRate)
}

// burnRate is the session's average spend per hour. Returns 0 until enough
// wall-clock has elapsed for the figure to be stable (avoids a huge $/hr in
// the first seconds of a session).
func (s *RunState) burnRate() float64 {
	if s.startedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(s.startedAt).Hours()
	if elapsed < 1.0/120 { // < 30s: too noisy to report
		return 0
	}
	return s.sessionCost() / elapsed
}

// tpsSeries / cacheSeries / costSeries project the history ring onto the
// float slices the dashboard sparklines consume. (Fill has no series: it's
// shown by the composition bar, not a sparkline, so it isn't duplicated.)
func (s *RunState) tpsSeries() []float64 {
	out := make([]float64, len(s.history))
	for i, h := range s.history {
		out[i] = h.tokPerSec
	}
	return out
}

func (s *RunState) cacheSeries() []float64 {
	out := make([]float64, len(s.history))
	for i, h := range s.history {
		if h.tokIn > 0 {
			out[i] = float64(h.cached) / float64(h.tokIn)
		}
	}
	return out
}

func (s *RunState) costSeries() []float64 {
	out := make([]float64, len(s.history))
	for i, h := range s.history {
		out[i] = h.costUSD
	}
	return out
}

// topTools returns the session's tools sorted by call count (then total
// duration, then name) — the order both the sidebar and dashboard render.
func (s *RunState) topTools() []toolRow {
	out := make([]toolRow, 0, len(s.toolStats))
	for name, st := range s.toolStats {
		out = append(out, toolRow{name: name, calls: st.calls, fails: st.fails, dur: st.dur})
	}
	slices.SortStableFunc(out, func(a, b toolRow) int {
		if a.calls != b.calls {
			if a.calls > b.calls {
				return -1
			}
			return 1
		}
		if a.dur != b.dur {
			if a.dur > b.dur {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.name, b.name)
	})
	return out
}

// toolRow is one row of the TOOLS section.
type toolRow struct {
	name  string
	calls int
	fails int
	dur   time.Duration
}

// compactionNotice formats the one-line transcript notice a compaction
// drops into the timeline, mirroring the cockpit's COMPACTION row.
func compactionNotice(before, after, bytes int, engine string) string {
	s := "↯ compacted " + strconv.Itoa(before) + "→" + strconv.Itoa(after) + " msgs"
	if bytes > 0 {
		s += " · " + fmtBytes(bytes) + " reclaimed"
	}
	if engine != "" {
		s += " · " + engine
	}
	return s
}

// lines renders the cockpit as a flat list of sidebar rows — the narrow /
// degenerate fallback used when the sidebar is too short for the full
// graphical cockpit (drawSidebar prefers the rich render when it fits).
func (s *RunState) lines() []string {
	status := palette.Muted.On("idle")
	if s.Running {
		status = palette.Success.On("running")
	}
	out := []string{
		row("status", status),
		row("iters", strconv.Itoa(s.iterations)),
	}
	if s.liveTotal > 0 {
		out = append(out, row("tokens", fmtCount(s.liveTotal)))
	}
	if s.liveCtx > 0 {
		out = append(out, row("ctx", fmtCount(s.liveCtx)))
	}
	tools := strconv.Itoa(s.tools)
	if s.toolsRunning > 0 {
		tools += palette.Warning.On(" (+" + strconv.Itoa(s.toolsRunning) + ")")
	}
	out = append(out, row("tools", tools))
	if s.maxDepth > 0 {
		out = append(out, row("agents", "d"+strconv.Itoa(s.maxDepth)))
	}
	return out
}

// row formats a dim, fixed-width label followed by its value.
func row(label, value string) string {
	return palette.Subtle.On(padRight(label, 8)) + value
}

// itoa is strconv.Itoa under a shorter name — the cockpit renderers call it
// constantly and the brevity keeps the row builders readable.
func itoa(n int) string { return strconv.Itoa(n) }

// padRight pads s with spaces to at least n display columns.
func padRight(s string, n int) string {
	w := ansi.StringWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}
