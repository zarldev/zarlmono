package taskrunner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// The zkit-backed task loop (executeTaskZkit) consumes three seams
// defined here and in the sibling files: a PromptSource, an EventSink
// (zkit_sink.go), and a per-task tool source (zkit_source.go). The
// legacy runAgentLoop (see executeTask) shares none of them.

// Prompt-var keys the runner threads through to the PromptSource on each
// Run. promptVarsFor populates them from the task + resolved profile so
// System reassembles the same system prompt buildPrompts produces today.
const (
	promptVarPrefix  = "profile_prefix"
	promptVarPerson  = "person"
	promptVarProfile = "profile"
	promptVarQuery   = "query"
)

// memoryLoader renders the requester's stored-memory system-prompt
// fragment, or "" when there's nothing to inject. Matches
// (*Runner).loadMemoryContext.
type memoryLoader func(ctx context.Context, person string) string

// skillLoader renders the profile-scoped skill fragment for a (profile,
// query) pair, or "" when no skills match. Matches
// (*Runner).loadSkillContext.
type skillLoader func(ctx context.Context, profile, query string) string

// zkitPromptSource resolves the task system prompt for the zkit runner.
// It mirrors (*Runner).buildPrompts' system-prompt half: base prompt +
// profile prefix + memory context + skill context, joined by blank
// lines. Pull-based per runner.PromptSource — base is read through a
// func so operator hot-edits of the runner's system prompt take effect
// on the next task without rebuilding the source.
type zkitPromptSource struct {
	base   func() string
	memory memoryLoader
	skills skillLoader
}

// System implements runner.PromptSource. The per-task values (profile
// prefix, person, profile name, query) arrive via vars so a single
// source instance serves every task. Never errors — the underlying
// loaders already degrade to "" on failure, matching buildPrompts'
// best-effort injection.
func (s zkitPromptSource) System(ctx context.Context, vars runner.PromptVars) (string, error) {
	var system string
	if s.base != nil {
		system = s.base()
	}
	system = appendBlock(system, vars.String(promptVarPrefix))
	if s.memory != nil {
		system = appendBlock(system, s.memory(ctx, vars.String(promptVarPerson)))
	}
	if s.skills != nil {
		system = appendBlock(system, s.skills(ctx, vars.String(promptVarProfile), vars.String(promptVarQuery)))
	}
	return system, nil
}

// newPromptSource binds a zkitPromptSource to the runner's live system
// prompt and its memory/skill loaders. Reading r.systemPrompt through
// the closure (rather than capturing the string) preserves the
// hot-reload semantics buildPrompts has today.
func (r *Runner) newPromptSource() zkitPromptSource {
	return zkitPromptSource{
		base:   func() string { return r.systemPrompt },
		memory: r.loadMemoryContext,
		skills: r.loadSkillContext,
	}
}

// taskPromptInput is the slice of repository.Task promptVarsFor needs —
// kept narrow so the prompt seam doesn't drag in the repository package
// and stays trivially testable.
type taskPromptInput struct {
	PersonName string
	Prompt     string
}

// promptVarsFor builds the runner.PromptVars for a task: the resolved
// profile's prefix + name, the requester, and the task goal (the query
// the skill selector matches against). Mirrors the inputs buildPrompts
// feeds loadMemoryContext / loadSkillContext.
func promptVarsFor(task taskPromptInput, resolved ResolvedProfile) runner.PromptVars {
	return runner.PromptVars{
		promptVarPrefix:  resolved.PromptPrefix,
		promptVarPerson:  task.PersonName,
		promptVarProfile: string(resolved.Name),
		promptVarQuery:   task.Prompt,
	}
}
