package compact

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ExecutiveDefaultMaxTokens caps the narrative section of the
// briefing. The structured sections (plan, files, tools) are size-
// bounded by their own data so the total briefing stays well under
// a few thousand tokens even on the largest histories.
const ExecutiveDefaultMaxTokens = 3000

// ExecutiveDefaultSystemPrompt instructs the briefing model. Targets
// a state-handoff voice — third-person, no fluff, every line
// load-bearing. The model's job is to make a fresh-context successor
// agent productive immediately.
const ExecutiveDefaultSystemPrompt = `You are an executive briefer. You receive the older portion of a coding-agent conversation that is being compacted to free context. The structured sections (PLAN PROGRESS, WORKING FILES, TOOL USAGE) are already attached at the top of the resulting briefing — DO NOT repeat them. Your job is to produce the NARRATIVE section.

Write a synthesis that a successor agent could read in 30 seconds and pick up exactly where the original left off. Cover:

  - The user's stated goal (in their words where possible).
  - Decisions the agent committed to and why (architecture, library, pattern choices).
  - Concrete intermediate findings the agent will need to reference (file contents, command output, error messages — paraphrased, not re-quoted).
  - Open questions the agent was about to ask or work that's mid-flight.
  - Pitfalls / dead ends already explored so the successor doesn't redo them.

No bullet recap of every turn. No "the user said X, the agent replied Y". One coherent paragraph or two of operational handoff prose. Aim for under 600 words.`

// PlanStep is one item in the plan snapshot the briefing renders.
// Status is one of "pending", "in_progress", "completed". Note is
// optional context the model added with the step.
type PlanStep struct {
	Title  string
	Status string
	Note   string
}

// FileTouch is one entry in the working-files snapshot. Path is the
// absolute path (or workspace-relative if absolute paths leak too
// much path noise). Action is "read", "write", "edit", or similar
// — the model uses it to tell what state the file was last in.
type FileTouch struct {
	Path   string
	Action string
}

// ToolUsage is one row in the tool histogram. Count is the call
// count over the session-so-far; Name is the registered tool name.
type ToolUsage struct {
	Name  string
	Count int
}

// StateProvider hands the executive compactor a snapshot of "what
// the agent is currently doing" — the structured state the shell
// already tracks. Each method returns the relevant view; empty /
// nil returns are tolerated (sections elide gracefully). All three
// are called once per Compact invocation; the snapshot is frozen
// from there.
type StateProvider interface {
	Plan() []PlanStep
	WorkingFiles() []FileTouch
	TopTools() []ToolUsage
}

// Executive is the structured-briefing compactor. Combines four
// pieces into a single assistant-role briefing message that replaces
// the older portion of history:
//
//  1. PLAN PROGRESS — pulled from update_plan via StateProvider.
//  2. WORKING FILES — recent reads / writes via StateProvider.
//  3. TOOL USAGE — top tools by call count via StateProvider.
//  4. NARRATIVE — LLM-produced synthesis of the older turns.
//
// The first three are mechanical projections of state the shell
// already maintains, so they're cheap and deterministic. The
// narrative requires an LLM call (typically against a larger
// EngineExecutive model the user has configured via CompactProvider/
// CompactModel) and produces the bit that summary alone can't:
// operational context a successor agent can act on.
//
// Trade-offs vs Summary:
//
//   - The structured sections are richer than prose alone — a
//     successor immediately knows "step 3 of 5, currently editing
//     pkg/foo.go, just ran tests, top tools have been read+grep".
//   - Costs the same LLM call as Summary; structured sections add
//     ~negligible overhead.
//   - Loses raw tool result bodies (same as Summary); the agent
//     re-runs the tool if needed.
type Executive struct {
	Provider     llm.Provider
	Model        string
	State        StateProvider
	SystemPrompt string // optional override; defaults to ExecutiveDefaultSystemPrompt
	MaxTokens    int    // optional override; defaults to ExecutiveDefaultMaxTokens
}

// NewExecutive constructs an Executive compactor. provider + model
// drive the narrative LLM call; state provides the structured
// sections (passing nil State elides those sections and falls back
// to a Summary-equivalent narrative-only briefing).
func NewExecutive(provider llm.Provider, model string, state StateProvider) *Executive {
	return &Executive{Provider: provider, Model: model, State: state}
}

// WouldReduceBytes implements [Prober]. Executive replaces every
// older message with a single structured briefing, so the savings
// upper-bound is the older slice's byte count (briefing length
// claws some back; net is almost always positive when the trigger
// fires). Mirrors [Summary.WouldReduceBytes] — the two engines have
// the same shape: "many older → one synthesised message.".
func (e *Executive) WouldReduceBytes(history []llm.Message, keepRecent int) int {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent+1 {
		return 0
	}
	older := history[:len(history)-keepRecent]
	total := 0
	for _, msg := range older {
		total += len(msg.Content)
	}
	return total
}

// Compact implements [Compactor]. Splits history at keepRecent,
// generates the briefing from older messages + structured state,
// returns a new history of (leading system msgs) + (1 briefing msg)
// + (keepRecent most-recent verbatim).
func (e *Executive) Compact(ctx context.Context, history []llm.Message, keepRecent int) (Result, error) {
	if e.Provider == nil {
		return Result{}, errors.New("compact.Executive: provider is nil")
	}
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent+1 {
		return Result{
			History: append([]llm.Message{}, history...),
			Engine:  EngineExecutive,
		}, nil
	}

	leading, older, recent := splitForSummary(history, keepRecent)
	if len(older) == 0 {
		return Result{
			History: append([]llm.Message{}, history...),
			Engine:  EngineExecutive,
		}, nil
	}

	// Build the structured sections from StateProvider first. These
	// go at the TOP of the briefing — a model scanning the message
	// for orientation hits the operational state before the prose.
	var plan, files, tools string
	if e.State != nil {
		plan = renderPlanSection(e.State.Plan())
		files = renderFilesSection(e.State.WorkingFiles())
		tools = renderToolsSection(e.State.TopTools())
	}

	sysPrompt := e.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = ExecutiveDefaultSystemPrompt
	}
	maxTokens := e.MaxTokens
	if maxTokens <= 0 {
		maxTokens = ExecutiveDefaultMaxTokens
	}

	rendered := renderOlderForSummary(older)
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: sysPrompt},
			{Role: llm.RoleUser, Content: rendered},
		},
		MaxTokens:   maxTokens,
		Temperature: 0.2,
		Stream:      true,
	}
	stream, err := e.Provider.Complete(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("compact.Executive: provider complete: %w", err)
	}
	var narrative strings.Builder
	for chunk, cerr := range stream {
		if cerr != nil {
			return Result{}, fmt.Errorf("compact.Executive: stream: %w", cerr)
		}
		narrative.WriteString(chunk.Content)
	}
	narrativeBody := strings.TrimSpace(narrative.String())
	if narrativeBody == "" {
		// Briefing without a narrative is still useful (structured
		// sections carry real signal), but flag it so the user knows
		// the LLM call returned nothing.
		narrativeBody = "(no narrative — briefing model returned empty content; structured state above is current)"
	}

	briefing := composeBriefing(len(older), plan, files, tools, narrativeBody)

	out := make([]llm.Message, 0, len(leading)+1+len(recent))
	out = append(out, leading...)
	out = append(out, llm.Message{Role: llm.RoleAssistant, Content: briefing})
	out = append(out, recent...)

	// Same orphan-tool repair as Summary.Compact — see the note there
	// for why this is load-bearing for collapse-style compactors.
	out, _ = RepairToolPairing(out)

	warning := fmt.Sprintf(
		"compacted via executive: %d older msgs → 1 briefing msg (~%d chars); %d recent kept verbatim",
		len(older),
		len(briefing),
		len(recent),
	)
	return Result{History: out, Warning: warning, Engine: EngineExecutive}, nil
}

// composeBriefing assembles the final briefing message from the
// structured sections + narrative. Sections elide cleanly when
// empty — a session with no plan / no files / no tools just gets
// the narrative.
func composeBriefing(olderCount int, plan, files, tools, narrative string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[compacted — executive briefing of %d older message(s)]\n\n", olderCount)
	if plan != "" {
		b.WriteString(plan)
		b.WriteString("\n\n")
	}
	if files != "" {
		b.WriteString(files)
		b.WriteString("\n\n")
	}
	if tools != "" {
		b.WriteString(tools)
		b.WriteString("\n\n")
	}
	b.WriteString("## NARRATIVE\n\n")
	b.WriteString(narrative)
	return b.String()
}

// renderPlanSection formats the plan steps as a markdown list with
// status icons. Empty input returns "" so the section elides.
func renderPlanSection(steps []PlanStep) string {
	if len(steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## PLAN PROGRESS\n")
	for i, s := range steps {
		icon := "▢"
		switch s.Status {
		case "completed":
			icon = "✓"
		case "in_progress":
			icon = "▶"
		}
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = fmt.Sprintf("step %d", i+1)
		}
		fmt.Fprintf(&b, "- %s %s", icon, title)
		if note := strings.TrimSpace(s.Note); note != "" {
			fmt.Fprintf(&b, " — %s", note)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderFilesSection formats recent file touches. De-duplicates by
// path, keeping the most recent action per file.
func renderFilesSection(touches []FileTouch) string {
	if len(touches) == 0 {
		return ""
	}
	seen := map[string]string{}
	order := []string{}
	for _, t := range touches {
		p := strings.TrimSpace(t.Path)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; !ok {
			order = append(order, p)
		}
		// Last-wins on the action — files often go read → edit → write
		// in a turn; the most recent action is the agent's current
		// view of the file's state.
		seen[p] = t.Action
	}
	if len(order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## WORKING FILES\n")
	for _, p := range order {
		action := seen[p]
		if action == "" {
			fmt.Fprintf(&b, "- %s\n", p)
		} else {
			fmt.Fprintf(&b, "- %s (last action: %s)\n", p, action)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderToolsSection formats the tool usage histogram, top 5 by
// count. Ties broken by name so the order is stable.
func renderToolsSection(usage []ToolUsage) string {
	if len(usage) == 0 {
		return ""
	}
	// Copy so we can sort without mutating the caller's slice.
	sorted := append([]ToolUsage(nil), usage...)
	slices.SortFunc(sorted, func(a, b ToolUsage) int {
		if a.Count != b.Count {
			if a.Count > b.Count {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	if len(sorted) > 5 {
		sorted = sorted[:5]
	}
	var b strings.Builder
	b.WriteString("## TOOL USAGE (this session)\n")
	for _, u := range sorted {
		fmt.Fprintf(&b, "- %s × %d\n", u.Name, u.Count)
	}
	return strings.TrimRight(b.String(), "\n")
}
