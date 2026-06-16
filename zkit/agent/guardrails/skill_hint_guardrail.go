package guardrails

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// SkillLookup is the per-consumer hook the SkillHintGuardrail uses
// to discover whether a given tool has a matching skill markdown
// file. Lookup returns the absolute path of the skill body (the
// path a `read` call would consume) and a bool indicating presence.
// Implementations must be safe for concurrent use — multiple Runs
// may share a single guardrail.
//
// zarlcode's SkillStore satisfies this through a tiny adapter (the
// shell wiring's skillHintLookup); other consumers can implement
// against any naming convention they want — there's no contract
// that skill names must equal tool names, just that Lookup returns
// the right path when they do.
type SkillLookup interface {
	Lookup(toolName string) (path string, ok bool)
}

// SkillHintGuardrail watches PostCall results for failures and,
// when the failing tool has a matching skill markdown file in the
// SkillLookup, appends a one-line read("<path>") hint to the
// existing error message. The hint points the model at the recovery
// recipe through zarlcode's existing discover-then-read pattern —
// the body isn't injected into the prompt; the model decides
// whether to pull it on the next turn.
//
// # Why mutation, not rejection
//
// Other guardrails (decompose, improvement, schema) wrap their
// signal via failedFromGuard, which produces a fresh Result whose
// Kind comes from the rejection error. That's wrong for this
// guardrail — the goal is augment, not classify. A Kinds.NOTFOUND
// failure (file doesn't exist) should stay Kinds.NOTFOUND after the
// hint is appended; reclassifying it to Kinds.VALIDATION would lie
// to downstream routers (decompose-budget, escalation policy)
// about what failure mode actually occurred. So this guardrail
// mutates result.Error in place and returns nil, preserving Kind.
//
// # Why idempotent
//
// In normal operation Inspect runs exactly once per result, but a
// pipeline that ever re-runs PostCall hooks (composed guardrails,
// retry layers) must not double-append. The presence check uses a
// fixed substring drawn from the path itself, which is unique enough
// for the duplicate-detection job.
type SkillHintGuardrail struct {
	skills SkillLookup
}

// NewSkillHintGuardrail wires the guardrail to a SkillLookup. A nil
// lookup makes the guardrail a no-op — the constructor never
// returns nil so callers can compose unconditionally.
func NewSkillHintGuardrail(skills SkillLookup) *SkillHintGuardrail {
	return &SkillHintGuardrail{skills: skills}
}

// Name returns the guardrail's identifier.
func (g *SkillHintGuardrail) Name() string { return "skill_hint" }

// Inspect appends a read-hint to result.Error when:
//   - the lookup is non-nil,
//   - the call produced a failed Result (not a hard execErr — that's
//     a separate channel the runner already surfaces),
//   - the failure is not a Permission denial (a skill won't fix that —
//     the recovery is a different cwd or different user, not the
//     skill's call shape), and
//   - a skill with the same name as the called tool exists, and
//   - the hint isn't already in the message (idempotent).
//
// On all other paths it returns nil without touching result.
//
// Permission filtering uses the typed result.Err field when present
// (and falls back to result.Kind when only the legacy Kind enum is
// populated by an older tool). errors.AsType on result.Err.Wrapped
// could surface a deeper sentinel if a tool wraps a real OS error,
// but the Kind discriminator is the load-bearing check today.
func (g *SkillHintGuardrail) Inspect(_ context.Context, call tools.ToolCall, result *tools.ToolResult, _ error) error {
	if g.skills == nil || result == nil || result.Success || result.Error == "" {
		return nil
	}
	if kind := failureKind(result); kind == tools.Kinds.PERMISSION {
		return nil
	}
	path, ok := g.skills.Lookup(call.ToolName.String())
	if !ok {
		return nil
	}
	hint := fmt.Sprintf(`read("%s")`, path)
	if strings.Contains(result.Error, hint) {
		return nil
	}
	result.Error = fmt.Sprintf(
		"%s\n\n(skill: the %s tool has a recovery recipe at %s — call %s before retrying.)",
		result.Error, call.ToolName, path, hint)
	return nil
}

// failureKind picks the Kind for a result from the typed Err field.
func failureKind(result *tools.ToolResult) tools.Kind {
	if result.Err != nil {
		return result.Err.Kind
	}
	return tools.Kinds.UNKNOWN
}
