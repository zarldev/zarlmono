// Package evaluator holds the SWE-bench run_evaluation interop shared by the
// scorer (swebench-eval/runner) and the in-process verify path
// (swebench-eval/harness). Both write a predictions file, shell out to
// `python -m swebench.harness.run_evaluation`, and parse its summary JSON;
// keeping those shapes in one place stops the two copies from silently
// drifting when the evaluator's output format changes. It lives under
// internal/ (not runner) so harness can use it without an import cycle.
package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Non-resolution sentinels classify a verdict that isn't a genuine test
// failure. Callers match with errors.Is rather than comparing strings.
var (
	// ErrEvaluatorError marks an instance the evaluator listed under
	// error_ids — it could not be graded at all (typically the eval
	// container failed to start, e.g. a corrupt prebuilt image). Distinct
	// from an honest unresolved (tests ran and failed): an evaluator error
	// is often a false negative worth retrying on a freshly-built local image.
	ErrEvaluatorError = errors.New("evaluator error")
	// ErrEmptyPatch marks an instance whose prediction patch was empty
	// (empty_patch_ids) — the agent produced no diff to grade.
	ErrEmptyPatch = errors.New("empty patch")
	// ErrIncomplete marks an instance the evaluator never finished
	// (incomplete_ids) — e.g. not submitted in this invocation.
	ErrIncomplete = errors.New("incomplete")
)

// Verdict is the per-instance result parsed from the evaluator's run report.
type Verdict struct {
	Resolved bool
	// Err classifies a non-resolved verdict that isn't a genuine test
	// failure — a sentinel (ErrEvaluatorError / ErrEmptyPatch /
	// ErrIncomplete) callers match with errors.Is. Nil for a clean
	// resolve AND for an honest unresolved (tests ran and failed).
	Err error
}

// Reason renders Err as a human/DB string — "" when Err is nil.
func (v Verdict) Reason() string {
	if v.Err == nil {
		return ""
	}
	return v.Err.Error()
}

// Prediction is one row of the predictions file swebench's run_evaluation
// consumes.
type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelPatch      string `json:"model_patch"`
	ModelNameOrPath string `json:"model_name_or_path"`
}

// WritePredictions marshals preds to path (0600) as the JSON array
// run_evaluation expects.
func WritePredictions(path string, preds []Prediction) error {
	body, err := json.MarshalIndent(preds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal predictions: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write predictions %q: %w", path, err)
	}
	return nil
}

// Summary mirrors what swebench's run_evaluation drops into its per-run
// summary JSON. We read the resolved/unresolved/empty/error/incomplete id
// lists. Verified against the schema_version=2 layout.
type Summary struct {
	ResolvedIDs   []string `json:"resolved_ids"`
	UnresolvedIDs []string `json:"unresolved_ids"`
	EmptyPatchIDs []string `json:"empty_patch_ids"`
	ErrorIDs      []string `json:"error_ids"`
	IncompleteIDs []string `json:"incomplete_ids"`
}

// ParseSummary reads the evaluator's summary JSON at path and maps each
// instance id to its Verdict.
func ParseSummary(path string) (map[string]Verdict, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read summary %q: %w", path, err)
	}
	var sum Summary
	if err := json.Unmarshal(body, &sum); err != nil {
		return nil, fmt.Errorf("parse summary: %w", err)
	}
	out := map[string]Verdict{}
	for _, id := range sum.ResolvedIDs {
		out[id] = Verdict{Resolved: true}
	}
	for _, id := range sum.UnresolvedIDs {
		out[id] = Verdict{Resolved: false}
	}
	for _, id := range sum.EmptyPatchIDs {
		out[id] = Verdict{Resolved: false, Err: ErrEmptyPatch}
	}
	for _, id := range sum.ErrorIDs {
		out[id] = Verdict{Resolved: false, Err: ErrEvaluatorError}
	}
	for _, id := range sum.IncompleteIDs {
		out[id] = Verdict{Resolved: false, Err: ErrIncomplete}
	}
	return out, nil
}

// EnsureAvailable probes for the swebench python package + a reachable docker
// daemon, returning a remediation-hinting error when the evaluator can't run —
// so a run fails fast at the start of scoring rather than partway through.
func EnsureAvailable(ctx context.Context, python string) error {
	check := exec.CommandContext(ctx, python, "-c", "import swebench")
	if out, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("swebench package not installed (run: %s -m pip install swebench)\n%s",
			python, strings.TrimSpace(string(out)))
	}
	docker := exec.CommandContext(ctx, "docker", "info")
	docker.Stdout = io.Discard
	docker.Stderr = io.Discard
	if err := docker.Run(); err != nil {
		return fmt.Errorf("docker daemon not reachable: %w", err)
	}
	return nil
}
