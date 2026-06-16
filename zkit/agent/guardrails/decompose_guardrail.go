package guardrails

import (
	"context"
	"fmt"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Default thresholds. Kept as constants so the constructor stays a
// single-knob (budget cap) call; if a consumer needs per-knob tuning
// we'll graduate to an options pattern.
const (
	// signatureNudgeAt is the same-signature failure count that
	// promotes the result from pass-through to advisory. Failures 1
	// and 2 pass through unchanged — the model gets to read the real
	// error and adjust on its own twice before the guardrail editorialises.
	signatureNudgeAt = 3
	// signatureFatalAt is the same-signature failure count that
	// terminates the retry loop for that signature. The model has
	// seen the advisory once and ignored it; further retries of the
	// identical call won't help.
	signatureFatalAt = 4
	// toolNudgeAt is the tool-wide failure count (across distinct
	// signatures of the same tool) that adds a "the tool itself may
	// be unreliable" hint. Independent of the signature counter — a
	// model that varies args on every retry would never trip the
	// signature path but can still trip this one.
	toolNudgeAt = 4
	// toolFatalAt is the tool-wide failure count that terminates the
	// retry loop for a tool regardless of signature. It backstops the
	// degenerate case the signature path can't see: a model that varies
	// args on every failing call (different offset/limit/path spelling)
	// so no single signature ever reaches signatureFatalAt, yet the same
	// tool keeps failing. Must stay above toolNudgeAt so the advisory
	// nudge always fires before the hard stop.
	toolFatalAt = 8
)

// VerdictAction is the closed set of recovery actions a VerdictJudge
// can pick at the advisory threshold. The string values are the JSON
// Schema enum that constrains the model's output when LLMVerdictJudge
// is wired — keep the spelling stable, downstream prompts and the
// schema both reference these constants by value.
type VerdictAction string

const (
	// ActionRetryUnchanged means the failure is likely transient
	// (flaky tool, race, transient network) and a verbatim retry is
	// the right move. The model effectively votes "no scope change".
	ActionRetryUnchanged VerdictAction = "retry_unchanged"

	// ActionSmallerScope means the call's target is too broad —
	// narrow to one file/function/line and try again.
	ActionSmallerScope VerdictAction = "smaller_scope"

	// ActionSwitchTool means the failing tool is the problem, not the
	// args — pick a different tool that achieves the same effect.
	ActionSwitchTool VerdictAction = "switch_tool"

	// ActionSpawnSubagent means the work is large enough that
	// delegating to spawn_agent with a narrower mandate beats more
	// in-context retries.
	ActionSpawnSubagent VerdictAction = "spawn_subagent"
)

// Valid reports whether v is one of the four known actions. Used to
// reject malformed judge output before it shapes an advisory.
func (v VerdictAction) Valid() bool {
	switch v {
	case ActionRetryUnchanged, ActionSmallerScope, ActionSwitchTool, ActionSpawnSubagent:
		return true
	}
	return false
}

// Verdict is what a VerdictJudge returns. Rationale is the free-text
// reasoning the model emitted before committing to Action; it lands
// inside the advisory parens so the model on the next turn sees the
// chain of thought, not just the verb.
type Verdict struct {
	Rationale string
	Action    VerdictAction
}

// VerdictInput is the context a judge receives. The guardrail builds
// it from the failing call + the running counter — judges are pure
// functions of this input plus whatever model they wrap.
type VerdictInput struct {
	Tool     tools.ToolName
	Args     tools.ToolParameters
	Error    string
	Attempts int
}

// VerdictJudge is the optional hook DecomposeGuardrail consults at
// the advisory threshold (sigCount == signatureNudgeAt). When nil,
// the guardrail emits today's deterministic advisory. When set, the
// judge's Verdict shapes a tailored advisory instead.
//
// Implementations must be safe for concurrent use — multiple Inspect
// calls within one task can land here at the same time when tool
// dispatches run in parallel.
type VerdictJudge interface {
	Judge(ctx context.Context, in VerdictInput) (Verdict, error)
}

// DecomposeGuardrail tracks per-task tool-call failures and adds soft
// hints when the agent looks stuck. Implements the "small models fail
// to fix big problems, succeed at fixing small ones" half of the
// zarlcode harness thesis — without being so prescriptive that it
// hijacks the model away from a valid recovery path (e.g. switching
// tools when the failing tool is itself broken).
//
// Two counters run side by side, both per-task:
//
//	signature counter — keyed by canonical (tool, args).
//	    1, 2     → pass-through. The model sees the original failure
//	               and usually adjusts args or switches strategy on
//	               its own. The guardrail stays out of the way.
//	    3        → advisory. The result still leads with the original
//	               error so the model retains the context to choose;
//	               the guardrail appends a non-prescriptive hint that
//	               smaller scope / different tool / narrower target
//	               might unblock things. Returned as Kinds.VALIDATION so
//	               downstream classifiers route it like any other
//	               "won't help to repeat the same args" failure.
//	    4+       → Fatal. The model already saw the advisory and
//	               retried the identical call anyway; further repeats
//	               aren't productive.
//
//	tool counter — keyed by tool name, accumulates across distinct
//	    signatures. Triggers an advisory at 4 failures of the same
//	    tool in one task, even if no single signature has hit 3. The
//	    hint here points at the tool ("this tool keeps failing — a
//	    different tool may produce the same effect"), not at scope.
//	    Fires once per (task, tool); after that the tool counter
//	    keeps incrementing but doesn't re-trigger so the advisory
//	    doesn't spam every call.
//
// A per-task hard cap on *distinct* signatures that reach the
// signature nudge prevents a degenerate task from spiraling through
// endless advisories. When the cap is exceeded, the guardrail
// returns Budget so a downstream escalation policy can detect the
// condition and switch model or terminate.
//
// Signature canonicalization is shared with MemoSource via
// CallSignature, so "same call" means semantically identical args
// regardless of map ordering.
type DecomposeGuardrail struct {
	maxDecompositions int

	// judge is the optional advisory shaper. Read without a lock —
	// callers wire it once at construction via WithJudge and then
	// never touch it again. If a future caller actually does need to
	// swap judges at runtime they'll need to add the protection then.
	judge VerdictJudge

	mu      sync.Mutex
	buckets map[taskscope.ID]*decomposeBucket
}

// NewDecomposeGuardrail wires up the guardrail with a cap on how
// many distinct call signatures may trigger the advisory within one
// task. maxDecompositions ≤ 0 defaults to 5 — generous but finite.
// A task that needs more is almost certainly out of zarlcode's
// productive range and should escalate.
func NewDecomposeGuardrail(maxDecompositions int) *DecomposeGuardrail {
	if maxDecompositions <= 0 {
		maxDecompositions = 5
	}
	return &DecomposeGuardrail{
		maxDecompositions: maxDecompositions,
		buckets:           make(map[taskscope.ID]*decomposeBucket),
	}
}

// WithJudge wires an optional VerdictJudge that the guardrail
// consults at the advisory threshold to derive a tailored next-step
// hint instead of the default static advisory. Returns the receiver
// for fluent chaining at construction:
//
//	g := guardrails.NewDecomposeGuardrail(0).WithJudge(myJudge)
//
// Pass nil to opt back out of the verdict path (useful for tests).
// Not safe for concurrent reconfiguration — call once during setup.
func (g *DecomposeGuardrail) WithJudge(j VerdictJudge) *DecomposeGuardrail {
	g.judge = j
	return g
}

// Name returns the guardrail's identifier.
func (g *DecomposeGuardrail) Name() string { return "decompose" }

// Before hard-blocks a call before dispatch once its signature or its
// tool has already crossed the fatal threshold in this task. This is
// the enforcement half of the guardrail: Inspect's advisory and Fatal
// results are conversation text the model is free to ignore and
// re-issue — and a weak model does exactly that, spinning the same
// failing call until the iteration cap. Before refuses to dispatch at
// all, so the tool never executes again: no wasted tokens, no repeated
// side-effect attempt, regardless of what the model decides next.
//
// Before never mutates the counters — it only reads what Inspect has
// already concluded. The advisory ladder (pass-through → nudge → Fatal)
// still runs entirely in Inspect; Before just makes the terminal rungs
// stick. It reads under the bucket lock for the same reason Inspect
// does: parallel dispatches within one task share the bucket.
func (g *DecomposeGuardrail) Before(ctx context.Context, call tools.ToolCall) error {
	bucket := g.bucketFor(taskscope.IDFrom(ctx))
	sig := tools.CallSignature(call)

	bucket.mu.Lock()
	sigCount := bucket.failures[sig]
	toolCount := bucket.toolFailures[call.ToolName]
	bucket.mu.Unlock()

	if sigCount >= signatureFatalAt {
		return tools.Fatal("decompose", fmt.Errorf(
			"refusing to re-dispatch: this exact call has already failed %d times in this task. "+
				"Change the arguments, switch to a different tool, or delegate to `spawn_agent`",
			sigCount))
	}
	if toolCount >= toolFatalAt {
		return tools.Budget("decompose", fmt.Sprintf(
			"refusing further %q calls: the tool has already failed %d times in this task across "+
				"distinct inputs. Switch to a different tool that produces the same effect or "+
				"delegate to `spawn_agent`",
			call.ToolName, toolCount))
	}
	return nil
}

// Inspect counts failures by call signature and (separately) by tool
// name, then rewrites repeat failures into advisory errors that lead
// with the original failure message. Successful results pass through
// untouched; the counters don't decrement on success.
//
// Counter mutations happen under bucket.mu; the lock is released
// before any judge call so a slow judge doesn't serialise other tool
// dispatches against the same task.
func (g *DecomposeGuardrail) Inspect(
	ctx context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	execErr error,
) error {
	if !isFailure(result, execErr) {
		return nil
	}
	bucket := g.bucketFor(taskscope.IDFrom(ctx))
	sig := tools.CallSignature(call)
	original := originalErrorMessage(result, execErr)
	kind := tupleFailureKind(result, execErr)

	bucket.mu.Lock()
	bucket.failures[sig]++
	bucket.toolFailures[call.ToolName]++
	if kind == tools.Kinds.VALIDATION {
		bucket.toolValidationFailures[call.ToolName]++
	}
	if kind == tools.Kinds.STALE {
		bucket.toolStaleFailures[call.ToolName]++
	}
	sigCount := bucket.failures[sig]
	toolCount := bucket.toolFailures[call.ToolName]
	// All of this tool's failures so far were malformed-input rejections
	// (no genuine tool/environment failure mixed in). When true, the
	// tool-wide advisory points at the input format rather than at
	// switching tools — switching can't fix args the model keeps getting
	// wrong.
	allValidation := bucket.toolValidationFailures[call.ToolName] == toolCount
	// All of this tool's failures so far were stale-anchor rejections — the
	// input was fine each time, the target kept moving. Re-reading fixes it;
	// fixing the format or switching tools does not.
	allStale := bucket.toolStaleFailures[call.ToolName] == toolCount
	// Pre-commit any state mutations the decision needs so we can
	// safely release the lock before doing slow work (judge calls).
	atNudge := sigCount == signatureNudgeAt
	toolFatal := toolCount >= toolFatalAt
	overCap := false
	if atNudge {
		bucket.triggered++
		overCap = bucket.triggered > g.maxDecompositions
	}
	firstToolNudge := sigCount < signatureNudgeAt &&
		toolCount == toolNudgeAt &&
		!bucket.toolNudged[call.ToolName]
	if firstToolNudge {
		bucket.toolNudged[call.ToolName] = true
	}
	triggered := bucket.triggered
	bucket.mu.Unlock()

	switch {
	case sigCount >= signatureFatalAt:
		// The model saw the advisory at sigCount=3 and retried the
		// same call anyway. Hard-stop the retry loop for this
		// signature; an upstream escalation policy can detect Fatal
		// and route differently.
		return tools.Fatal("decompose", fmt.Errorf(
			"call has failed %d times despite advisory; original: %s",
			sigCount, original))

	case toolFatal:
		// No single signature reached the fatal threshold — the model
		// kept varying args — but the same tool has now failed enough
		// times across distinct inputs that more retries won't help.
		// Budget (not Fatal) so an escalation policy reads it as "this
		// tool is exhausted for the task" rather than "this one call is
		// dead".
		return tools.Budget("decompose", fmt.Sprintf(
			"the %q tool has now failed %d times in this task across distinct inputs (cap %d) — "+
				"retrying it won't help. Switch to a different tool that produces the same effect "+
				"or delegate to `spawn_agent`. Original: %s",
			call.ToolName, toolCount, toolFatalAt, original))

	case atNudge:
		if overCap {
			return tools.Budget("decompose", fmt.Sprintf(
				"task triggered the advisory %d times — exceeded cap (%d). Escalate or abort.",
				triggered, g.maxDecompositions))
		}
		return tools.Validation("decompose", g.signatureAdvisory(ctx, call, original, sigCount, kind))

	default:
		// Signature counter is in pass-through (1 or 2). The tool
		// counter may still want to add a hint if this same tool has
		// been failing across different inputs.
		if firstToolNudge {
			return tools.Validation("decompose", toolNudgeAdvisory(original, call.ToolName, toolCount, allValidation, allStale))
		}
		// First, second, or post-nudge failure — pass through. Most
		// tool failures resolve on the next retry once the model sees
		// the error.
		return nil
	}
}

// signatureAdvisory builds the advisory body at the signature nudge
// threshold. When no judge is wired it returns the deterministic
// "consider smaller scope / different tool / spawn_agent" framing
// the harness has always used. When a judge is wired and returns a
// valid verdict, the advisory tailors to that single recommendation
// instead of asking the model to pick three options off a list.
//
// Judge failure (network, bad JSON, invalid action) is treated as
// "no verdict available" and falls back to the deterministic
// advisory. Never propagate a judge error up — the guardrail's
// contract is to add advisory context, not to introduce a new
// failure mode that didn't exist in the deterministic path.
func (g *DecomposeGuardrail) signatureAdvisory(
	ctx context.Context,
	call tools.ToolCall,
	original string,
	sigCount int,
	kind tools.Kind,
) string {
	if g.judge == nil {
		return defaultSignatureAdvisory(original, sigCount, kind)
	}
	verdict, err := g.judge.Judge(ctx, VerdictInput{
		Tool:     call.ToolName,
		Args:     call.Arguments,
		Error:    original,
		Attempts: sigCount,
	})
	if err != nil || !verdict.Action.Valid() {
		return defaultSignatureAdvisory(original, sigCount, kind)
	}
	return formatVerdictAdvisory(original, sigCount, verdict)
}

// defaultSignatureAdvisory is the unconditional advisory used when no
// judge is wired or the judge failed. A Validation failure means the
// tool rejected the input as malformed — repeating it or switching
// tools won't help, so the hint points at the input format. Any other
// kind keeps the original "smaller scope / different tool / spawn_agent"
// framing.
func defaultSignatureAdvisory(original string, sigCount int, kind tools.Kind) string {
	if kind == tools.Kinds.STALE {
		return fmt.Sprintf(
			"%s (advisory: this call has now failed %d times because the target moved "+
				"under it — the file changed since you last read it, so your line/hash "+
				"anchors are stale. Re-run read on this file to get fresh anchors, then "+
				"retry; the input format is fine and switching tools won't help. The "+
				"original error is the source of truth.)",
			original, sigCount)
	}
	if kind == tools.Kinds.VALIDATION {
		return fmt.Sprintf(
			"%s (advisory: this call has now failed validation %d times with the same "+
				"input. The tool rejected your arguments as malformed — re-read the "+
				"failure above and fix the input format (escaping, required fields, the "+
				"expected patch/edit shape); resending the same args or switching tools "+
				"won't help. The original error is the source of truth.)",
			original, sigCount)
	}
	return fmt.Sprintf(
		"%s (advisory: this call has now failed %d times — consider a smaller scope "+
			"(one line/function/file), a different tool that produces the same effect, "+
			"or delegating to `spawn_agent` with a narrower question. Pick whichever "+
			"fits — the original error above is the source of truth.)",
		original, sigCount)
}

// toolNudgeAdvisory builds the tool-wide hint that fires when one tool
// has failed across several distinct inputs. When every one of those
// failures was a malformed-input rejection (allValidation), the tool is
// working fine and the model keeps handing it bad input — so the hint
// points at the format, not at switching tools. Otherwise the tool may
// genuinely be unreliable on this target and switching is worth a shot.
func toolNudgeAdvisory(original string, tool tools.ToolName, toolCount int, allValidation, allStale bool) string {
	if allStale {
		return fmt.Sprintf(
			"%s (advisory: the %q tool has now failed %d times in this task because the "+
				"target kept moving — your line/hash anchors are stale. Re-run read on the "+
				"file before each edit so the anchors are fresh; the input format is fine "+
				"and switching tools won't help. The original error is the source of truth.)",
			original, tool, toolCount)
	}
	if allValidation {
		return fmt.Sprintf(
			"%s (advisory: the %q tool has now rejected %d different inputs as malformed "+
				"in this task. The tool is working — your input format is the problem. "+
				"Re-read the tool's expected format and the errors above before retrying; "+
				"switching tools won't fix malformed input. The original error is the "+
				"source of truth.)",
			original, tool, toolCount)
	}
	return fmt.Sprintf(
		"%s (advisory: the %q tool has now failed %d times in this task across "+
			"different inputs. The tool itself may be unreliable here — consider "+
			"switching to a different tool that produces the same effect, or "+
			"delegating the work to `spawn_agent`. The original error above is "+
			"the source of truth.)",
		original, tool, toolCount)
}

// formatVerdictAdvisory renders a tailored advisory built from the
// judge's verdict. The action's recommendation lands first, the
// model's own rationale follows in parens so the next turn sees the
// reasoning rather than just the verb. Original error stays at the
// front — same lead-with-the-real-error contract as the deterministic
// path.
func formatVerdictAdvisory(original string, sigCount int, v Verdict) string {
	rec := verdictRecommendation(v.Action)
	if v.Rationale == "" {
		return fmt.Sprintf("%s (advisory: this call has now failed %d times — %s)",
			original, sigCount, rec)
	}
	return fmt.Sprintf("%s (advisory: this call has now failed %d times — %s. Reason: %s)",
		original, sigCount, rec, v.Rationale)
}

// verdictRecommendation maps each VerdictAction to the imperative
// sentence the model should read on the next turn. Kept separate
// from formatVerdictAdvisory so a future tweak to the wording (or a
// new action) lives in one switch.
func verdictRecommendation(a VerdictAction) string {
	switch a {
	case ActionRetryUnchanged:
		return "this failure looks transient; retry the same call once more"
	case ActionSmallerScope:
		return "narrow the scope (one line / one function / one file) and try again"
	case ActionSwitchTool:
		return "switch to a different tool that produces the same effect — this tool keeps failing on this target"
	case ActionSpawnSubagent:
		return "delegate to `spawn_agent` with a narrower mandate — this work is bigger than the current call can land"
	}
	return "consider a smaller scope, a different tool, or delegating to `spawn_agent`"
}

// ForgetTask drops the per-task bucket for id. Long-lived runners
// that serve many short tasks call this from a task-completion
// observer to keep memory bounded; for short-lived (one-task)
// runners it's optional.
func (g *DecomposeGuardrail) ForgetTask(id taskscope.ID) {
	g.mu.Lock()
	delete(g.buckets, id)
	g.mu.Unlock()
}
