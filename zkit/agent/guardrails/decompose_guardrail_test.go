package guardrails_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func failingResult(msg string) *tools.ToolResult {
	return &tools.ToolResult{Success: false, Error: msg}
}

func successResult() *tools.ToolResult {
	return &tools.ToolResult{Success: true, Data: "ok"}
}

// validationFailing is a failed result classified as a malformed-input
// rejection (Kind=Validation), the shape tools.Failure(tools.Validation(...))
// produces. The advisory text branches on this kind.
func validationFailing(msg string) *tools.ToolResult {
	return &tools.ToolResult{Success: false, Error: msg, Err: tools.Validation("decompose", msg)}
}

// staleFailing is a failed result classified as a stale-anchor rejection
// (Kind=Stale): well-formed args whose target moved since the file was read.
// The advisory text must steer toward re-reading, not fixing the format.
func staleFailing() *tools.ToolResult {
	const msg = "hash mismatch"
	return &tools.ToolResult{Success: false, Error: msg, Err: tools.Stale("decompose", msg)}
}

func callWithArgs(toolName, id string, args tools.ToolParameters) tools.ToolCall {
	return tools.ToolCall{ID: tools.ToolCallID(id), ToolName: tools.ToolName(toolName), Arguments: args}
}

func TestDecomposeGuardrail_FirstTwoFailuresPassThrough(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("write", "c1", tools.ToolParameters{"path": "foo.go"})

	for i := 1; i <= 2; i++ {
		if err := g.Inspect(t.Context(), call, failingResult("tool failed"), nil); err != nil {
			t.Errorf("failure %d: want pass-through, got %v", i, err)
		}
	}
}

func TestDecomposeGuardrail_ThirdFailureTriggersAdvisory(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	err := g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	if err == nil {
		t.Fatal("third failure: want advisory rejection")
	}
	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", e.Kind)
	}
	if !strings.Contains(e.Reason, "tool failed") {
		t.Errorf("Reason = %q, want it to lead with the original error", e.Reason)
	}
	if !strings.HasPrefix(e.Reason, "tool failed") {
		t.Errorf("Reason = %q, advisory should lead with the original error (not wrap it)", e.Reason)
	}
	if !strings.Contains(e.Reason, "advisory") {
		t.Errorf("Reason = %q, want the advisory framing", e.Reason)
	}
}

func TestDecomposeGuardrail_FourthFailureIsFatal(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	_ = g.Inspect(t.Context(), call, failingResult("err1"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("err2"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("err3"), nil)
	err := g.Inspect(t.Context(), call, failingResult("err4"), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.FATAL {
		t.Errorf("Kind = %v, want Fatal", e.Kind)
	}
}

func TestDecomposeGuardrail_DifferentSignaturesIndependent(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call1 := callWithArgs("write", "c1", tools.ToolParameters{"path": "foo.go"})
	call2 := callWithArgs("write", "c2", tools.ToolParameters{"path": "bar.go"})

	// First failure of each — both should pass through.
	if err := g.Inspect(t.Context(), call1, failingResult("e"), nil); err != nil {
		t.Errorf("call1 first failure: want pass, got %v", err)
	}
	if err := g.Inspect(t.Context(), call2, failingResult("e"), nil); err != nil {
		t.Errorf("call2 first failure: want pass, got %v", err)
	}
}

func TestDecomposeGuardrail_ArgOrderingProducesSameSig(t *testing.T) {
	// Two calls with the same args in different map order should
	// share a signature counter — CallSignature canonicalizes.
	g := guardrails.NewDecomposeGuardrail(0)
	a := callWithArgs("edit", "c1", tools.ToolParameters{"path": "x", "old_string": "a"})
	b := callWithArgs("edit", "c2", tools.ToolParameters{"old_string": "a", "path": "x"})

	_ = g.Inspect(t.Context(), a, failingResult("e"), nil)
	_ = g.Inspect(t.Context(), b, failingResult("e"), nil)
	err := g.Inspect(t.Context(), a, failingResult("e"), nil)
	if err == nil {
		t.Fatal("re-ordered args: third call should hit advisory, not pass through")
	}
}

func TestDecomposeGuardrail_SuccessDoesNotIncrement(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	if err := g.Inspect(t.Context(), call, successResult(), nil); err != nil {
		t.Errorf("success: want pass, got %v", err)
	}
	// First two real failures should both pass through (counter is 1 then 2).
	if err := g.Inspect(t.Context(), call, failingResult("e"), nil); err != nil {
		t.Errorf("success then failure 1: want pass, got %v", err)
	}
	if err := g.Inspect(t.Context(), call, failingResult("e"), nil); err != nil {
		t.Errorf("success then failure 2: want pass, got %v", err)
	}
}

func TestDecomposeGuardrail_ExecErrorCountsAsFailure(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("bash", "c", tools.ToolParameters{"command": "ls"})

	_ = g.Inspect(t.Context(), call, nil, errors.New("boom"))
	_ = g.Inspect(t.Context(), call, nil, errors.New("boom"))
	err := g.Inspect(t.Context(), call, nil, errors.New("boom"))
	if err == nil {
		t.Fatal("three exec errors: want advisory")
	}
}

func TestDecomposeGuardrail_BudgetExceededAfterCap(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(2)
	// Trigger the advisory on three distinct signatures — cap is 2.
	for i, path := range []string{"a.go", "b.go", "c.go"} {
		call := callWithArgs("write", "c", tools.ToolParameters{"path": path})
		_ = g.Inspect(t.Context(), call, failingResult("e"), nil)
		_ = g.Inspect(t.Context(), call, failingResult("e"), nil)
		err := g.Inspect(t.Context(), call, failingResult("e"), nil)
		if i < 2 {
			if e, _ := errors.AsType[*tools.Error](err); e == nil || e.Kind != tools.Kinds.VALIDATION {
				t.Errorf("call %d: want Validation, got %v", i, err)
			}
		} else {
			e, ok := errors.AsType[*tools.Error](err)
			if !ok || e.Kind != tools.Kinds.BUDGET {
				t.Errorf("over-cap call: want Budget, got %v", err)
			}
		}
	}
}

func TestDecomposeGuardrail_ToolScopedAdvisoryAcrossSignatures(t *testing.T) {
	// Four distinct signatures of the same tool, each failing once.
	// No signature hits the per-signature nudge (which needs 3), but
	// the tool-wide counter does — the fourth failure should add an
	// advisory naming the tool itself.
	g := guardrails.NewDecomposeGuardrail(0)
	paths := []string{"a.go", "b.go", "c.go", "d.go"}
	for i, p := range paths[:3] {
		call := callWithArgs("apply_patch", "c", tools.ToolParameters{"path": p})
		if err := g.Inspect(t.Context(), call, failingResult("patch failed"), nil); err != nil {
			t.Errorf("failure %d (signature %q): want pass-through, got %v", i+1, p, err)
		}
	}
	call := callWithArgs("apply_patch", "c", tools.ToolParameters{"path": paths[3]})
	err := g.Inspect(t.Context(), call, failingResult("patch failed"), nil)
	if err == nil {
		t.Fatal("fourth distinct-signature failure: want tool-scoped advisory")
	}
	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", e.Kind)
	}
	if !strings.HasPrefix(e.Reason, "patch failed") {
		t.Errorf("Reason = %q, advisory should lead with the original error", e.Reason)
	}
	if !strings.Contains(e.Reason, "apply_patch") {
		t.Errorf("Reason = %q, tool advisory should name the tool", e.Reason)
	}
	if !strings.Contains(e.Reason, "switching to a different tool") {
		t.Errorf("Reason = %q, tool advisory should suggest switching tools", e.Reason)
	}
}

func TestDecomposeGuardrail_ValidationSignatureAdvisoryPointsAtInput(t *testing.T) {
	// The same call rejected as malformed three times: the advisory must
	// point at fixing the input format, NOT at smaller scope / switching
	// tools — the tool is working, the args are wrong.
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("apply_patch", "c", tools.ToolParameters{"patch": "*** Update x"})

	_ = g.Inspect(t.Context(), call, validationFailing("parse: bad header"), nil)
	_ = g.Inspect(t.Context(), call, validationFailing("parse: bad header"), nil)
	err := g.Inspect(t.Context(), call, validationFailing("parse: bad header"), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if !strings.HasPrefix(e.Reason, "parse: bad header") {
		t.Errorf("Reason = %q, advisory must lead with the original error", e.Reason)
	}
	if !strings.Contains(e.Reason, "malformed") || !strings.Contains(e.Reason, "input format") {
		t.Errorf("Reason = %q, validation advisory should point at the input format", e.Reason)
	}
	if strings.Contains(e.Reason, "smaller scope") {
		t.Errorf("Reason = %q, validation advisory must not suggest smaller scope", e.Reason)
	}
}

func TestDecomposeGuardrail_AllValidationToolNudgePointsAtFormat(t *testing.T) {
	// Four distinct inputs, every one rejected as malformed: the tool-wide
	// nudge must NOT say "switch tools" (the tool works fine) — it should
	// tell the model to fix its input format.
	g := guardrails.NewDecomposeGuardrail(0)
	for _, p := range []string{"*** Update a", "*** Add b", "*** Delete c"} {
		call := callWithArgs("apply_patch", "c", tools.ToolParameters{"patch": p})
		if err := g.Inspect(t.Context(), call, validationFailing("parse error"), nil); err != nil {
			t.Errorf("input %q: want pass-through, got %v", p, err)
		}
	}
	call := callWithArgs("apply_patch", "c", tools.ToolParameters{"patch": "*** Update d"})
	err := g.Inspect(t.Context(), call, validationFailing("parse error"), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if !strings.Contains(e.Reason, "apply_patch") {
		t.Errorf("Reason = %q, advisory should name the tool", e.Reason)
	}
	if !strings.Contains(e.Reason, "malformed") {
		t.Errorf("Reason = %q, all-validation nudge should call out malformed input", e.Reason)
	}
	if strings.Contains(e.Reason, "switching to a different tool") {
		t.Errorf("Reason = %q, all-validation nudge must NOT suggest switching tools", e.Reason)
	}
}

func TestDecomposeGuardrail_StaleSignatureAdvisoryPointsAtReread(t *testing.T) {
	// The same edit fails three times because the anchor went stale: the
	// advisory must tell the model to re-read the file, NOT to fix its input
	// format or switch tools — the args were fine each time.
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("edit", "c", tools.ToolParameters{"path": "README.md", "start_line": 141})

	_ = g.Inspect(t.Context(), call, staleFailing(), nil)
	_ = g.Inspect(t.Context(), call, staleFailing(), nil)
	err := g.Inspect(t.Context(), call, staleFailing(), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if !strings.HasPrefix(e.Reason, "hash mismatch") {
		t.Errorf("Reason = %q, advisory must lead with the original error", e.Reason)
	}
	if !strings.Contains(strings.ToLower(e.Reason), "re-run read") {
		t.Errorf("Reason = %q, stale advisory should tell the model to re-read", e.Reason)
	}
	if strings.Contains(e.Reason, "malformed") {
		t.Errorf("Reason = %q, stale advisory must not blame the input as malformed", e.Reason)
	}
}

func TestDecomposeGuardrail_AllStaleToolNudgePointsAtReread(t *testing.T) {
	// Four distinct edits, every one a stale-anchor rejection: the tool-wide
	// nudge must point at re-reading, never at switching tools or the format.
	g := guardrails.NewDecomposeGuardrail(0)
	for _, ln := range []int{10, 20, 30} {
		call := callWithArgs("edit", "c", tools.ToolParameters{"path": "README.md", "start_line": ln})
		if err := g.Inspect(t.Context(), call, staleFailing(), nil); err != nil {
			t.Errorf("line %d: want pass-through, got %v", ln, err)
		}
	}
	call := callWithArgs("edit", "c", tools.ToolParameters{"path": "README.md", "start_line": 40})
	err := g.Inspect(t.Context(), call, staleFailing(), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if !strings.Contains(strings.ToLower(e.Reason), "re-run read") {
		t.Errorf("Reason = %q, all-stale nudge should tell the model to re-read", e.Reason)
	}
	if strings.Contains(e.Reason, "switching to a different tool") || strings.Contains(e.Reason, "malformed") {
		t.Errorf("Reason = %q, all-stale nudge must not suggest switching tools or blame the format", e.Reason)
	}
}

func TestDecomposeGuardrail_MixedFailuresToolNudgeSuggestsSwitch(t *testing.T) {
	// One genuine (non-validation) failure mixed in with validation ones:
	// the tool may actually be unreliable, so the nudge keeps the
	// "switch tools" framing.
	g := guardrails.NewDecomposeGuardrail(0)
	mixed := []*tools.ToolResult{
		validationFailing("parse error"),
		failingResult("disk I/O error"), // Kind=UNKNOWN, not validation
		validationFailing("parse error"),
	}
	for i, res := range mixed {
		call := callWithArgs("apply_patch", "c", tools.ToolParameters{"patch": string(rune('a' + i))})
		if err := g.Inspect(t.Context(), call, res, nil); err != nil {
			t.Errorf("failure %d: want pass-through, got %v", i, err)
		}
	}
	call := callWithArgs("apply_patch", "c", tools.ToolParameters{"patch": "d"})
	err := g.Inspect(t.Context(), call, validationFailing("parse error"), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if !strings.Contains(e.Reason, "switching to a different tool") {
		t.Errorf("Reason = %q, mixed failures should keep the switch-tools framing", e.Reason)
	}
}

func TestDecomposeGuardrail_ToolAdvisoryFiresOnce(t *testing.T) {
	// After the tool-scoped advisory fires once, subsequent failures
	// of new signatures on the same tool should pass through again —
	// no advisory spam on every call.
	g := guardrails.NewDecomposeGuardrail(0)
	for i, p := range []string{"a.go", "b.go", "c.go"} {
		call := callWithArgs("apply_patch", "c", tools.ToolParameters{"path": p})
		if err := g.Inspect(t.Context(), call, failingResult("e"), nil); err != nil {
			t.Errorf("failure %d: want pass-through, got %v", i+1, err)
		}
	}
	// Fourth fires the advisory.
	c4 := callWithArgs("apply_patch", "c", tools.ToolParameters{"path": "d.go"})
	if err := g.Inspect(t.Context(), c4, failingResult("e"), nil); err == nil {
		t.Fatal("fourth failure: want advisory")
	}
	// Fifth (new signature) should pass through — advisory already fired.
	c5 := callWithArgs("apply_patch", "c", tools.ToolParameters{"path": "e.go"})
	if err := g.Inspect(t.Context(), c5, failingResult("e"), nil); err != nil {
		t.Errorf("fifth failure (new signature, post-advisory): want pass-through, got %v", err)
	}
}

func TestDecomposeGuardrail_DifferentToolsIndependent(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	// Three failures on each of two different tools — neither tool
	// crosses the tool-wide nudge (4), and no signature crosses the
	// signature nudge (3) since each call has unique args.
	for _, tool := range []string{"write", "edit"} {
		for _, p := range []string{"a", "b", "c"} {
			call := callWithArgs(tool, "c", tools.ToolParameters{"path": p})
			if err := g.Inspect(t.Context(), call, failingResult("e"), nil); err != nil {
				t.Errorf("tool=%s path=%s: want pass-through, got %v", tool, p, err)
			}
		}
	}
}

// fakeJudge satisfies VerdictJudge for tests. Returns the canned
// verdict or err on every call, without touching a network.
type fakeJudge struct {
	verdict guardrails.Verdict
	err     error
	calls   int
}

func (f *fakeJudge) Judge(_ context.Context, _ guardrails.VerdictInput) (guardrails.Verdict, error) {
	f.calls++
	if f.err != nil {
		return guardrails.Verdict{}, f.err
	}
	return f.verdict, nil
}

func TestDecomposeGuardrail_JudgeShapesAdvisory(t *testing.T) {
	judge := &fakeJudge{
		verdict: guardrails.Verdict{
			Action:    guardrails.ActionSpawnSubagent,
			Rationale: "the diff spans five files; a sub-agent can chunk it",
		},
	}
	g := guardrails.NewDecomposeGuardrail(0).WithJudge(judge)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	err := g.Inspect(t.Context(), call, failingResult("tool failed"), nil)

	if judge.calls != 1 {
		t.Errorf("judge.calls = %d, want 1 (only the nudge threshold should consult the judge)", judge.calls)
	}
	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Kind = %v, want Validation", e.Kind)
	}
	if !strings.HasPrefix(e.Reason, "tool failed") {
		t.Errorf("Reason = %q, advisory must still lead with the original error", e.Reason)
	}
	if !strings.Contains(e.Reason, "spawn_agent") {
		t.Errorf("Reason = %q, spawn_subagent verdict should mention spawn_agent", e.Reason)
	}
	if !strings.Contains(e.Reason, "five files") {
		t.Errorf("Reason = %q, rationale should appear in the advisory body", e.Reason)
	}
	if strings.Contains(e.Reason, "smaller scope") {
		t.Errorf(
			"Reason = %q, judge-shaped advisory should NOT include the deterministic three-option fallback",
			e.Reason,
		)
	}
}

func TestDecomposeGuardrail_JudgeErrorFallsBackToDefault(t *testing.T) {
	judge := &fakeJudge{err: errors.New("provider down")}
	g := guardrails.NewDecomposeGuardrail(0).WithJudge(judge)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	err := g.Inspect(t.Context(), call, failingResult("tool failed"), nil)

	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("err is %T, want *tools.Error", err)
	}
	if !strings.Contains(e.Reason, "smaller scope") {
		t.Errorf("Reason = %q, expected fallback to deterministic three-option advisory", e.Reason)
	}
}

func TestDecomposeGuardrail_JudgeInvalidActionFallsBack(t *testing.T) {
	// A malformed action (e.g. the model returned an unconstrained
	// string against a non-grammar-capable provider) must not poison
	// the advisory — fall back to deterministic instead.
	judge := &fakeJudge{verdict: guardrails.Verdict{Action: "wat", Rationale: "..."}}
	g := guardrails.NewDecomposeGuardrail(0).WithJudge(judge)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("tool failed"), nil)
	err := g.Inspect(t.Context(), call, failingResult("tool failed"), nil)

	e, _ := errors.AsType[*tools.Error](err)
	if e == nil || !strings.Contains(e.Reason, "smaller scope") {
		t.Errorf("Reason = %v, invalid action should fall back to deterministic advisory", err)
	}
}

func TestDecomposeGuardrail_JudgeNotCalledBelowNudge(t *testing.T) {
	judge := &fakeJudge{verdict: guardrails.Verdict{Action: guardrails.ActionSmallerScope}}
	g := guardrails.NewDecomposeGuardrail(0).WithJudge(judge)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	// First two failures pass through; the judge must not be consulted
	// (network calls on every failure would torch latency budgets).
	_ = g.Inspect(t.Context(), call, failingResult("e"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("e"), nil)
	if judge.calls != 0 {
		t.Errorf("judge.calls = %d after two pass-through failures, want 0", judge.calls)
	}
}

func TestDecomposeGuardrail_ForgetTaskResetsCounter(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("write", "c", tools.ToolParameters{"path": "foo.go"})

	_ = g.Inspect(t.Context(), call, failingResult("e"), nil)
	_ = g.Inspect(t.Context(), call, failingResult("e"), nil)
	g.ForgetTask("") // ctx has no TaskID — empty TaskID is the bucket key
	// After forget, the next two failures should again pass through.
	if err := g.Inspect(t.Context(), call, failingResult("e"), nil); err != nil {
		t.Errorf("after ForgetTask failure 1: want pass, got %v", err)
	}
	if err := g.Inspect(t.Context(), call, failingResult("e"), nil); err != nil {
		t.Errorf("after ForgetTask failure 2: want pass, got %v", err)
	}
}

// Before is the enforcement half: once a signature has failed enough to
// be Fatal in Inspect, Before refuses to dispatch the identical call at
// all (so the tool never re-executes), instead of leaving the model free
// to ignore the advisory and retry.
func TestDecomposeGuardrail_BeforeBlocksFatalSignature(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	call := callWithArgs("read", "c", tools.ToolParameters{"path": "README.md"})

	// Before the call has ever failed, dispatch is allowed.
	if err := g.Before(t.Context(), call); err != nil {
		t.Fatalf("Before on a fresh call: want pass, got %v", err)
	}
	// Drive the signature past the fatal threshold via Inspect.
	for range 4 {
		_ = g.Inspect(t.Context(), call, failingResult("read failed"), nil)
	}
	// Now Before must hard-block the identical call before dispatch.
	err := g.Before(t.Context(), call)
	e, ok := errors.AsType[*tools.Error](err)
	if !ok {
		t.Fatalf("Before after fatal: err is %T, want *tools.Error", err)
	}
	if e.Kind != tools.Kinds.FATAL {
		t.Errorf("Before Kind = %v, want Fatal", e.Kind)
	}
}

// A model that varies args on every failing call never trips the
// per-signature path, but the tool-wide counter still terminates the
// loop once the same tool has failed toolFatalAt times across distinct
// inputs. The terminal verdict is Budget (the tool is exhausted), and
// Before then refuses further calls to that tool.
func TestDecomposeGuardrail_ToolWideFatalAcrossSignatures(t *testing.T) {
	g := guardrails.NewDecomposeGuardrail(0)
	ctx := t.Context()

	// Eight distinct signatures of the same tool, each failing once — no
	// single signature reaches the fatal threshold.
	var last error
	for i := range 8 {
		call := callWithArgs("read", "c", tools.ToolParameters{"path": string(rune('a' + i))})
		last = g.Inspect(ctx, call, failingResult("read failed"), nil)
	}
	e, ok := errors.AsType[*tools.Error](last)
	if !ok {
		t.Fatalf("8th distinct failure: err is %T, want *tools.Error", last)
	}
	if e.Kind != tools.Kinds.BUDGET {
		t.Errorf("tool-wide terminal Kind = %v, want Budget", e.Kind)
	}
	// A brand-new signature for the same tool is now blocked pre-dispatch.
	fresh := callWithArgs("read", "c", tools.ToolParameters{"path": "never-tried.md"})
	blocked := g.Before(ctx, fresh)
	be, ok := errors.AsType[*tools.Error](blocked)
	if !ok {
		t.Fatalf("Before on exhausted tool: err is %T, want *tools.Error", blocked)
	}
	if be.Kind != tools.Kinds.BUDGET {
		t.Errorf("Before on exhausted tool Kind = %v, want Budget", be.Kind)
	}
}
