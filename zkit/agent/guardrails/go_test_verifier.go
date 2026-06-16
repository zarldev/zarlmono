package guardrails

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GoTestVerifier runs `go test` against the package(s) affected by a
// tool call's edits. Stronger signal than GoVerifier (which only runs
// `go vet`) but materially slower — every edit pays the
// per-package test cost. Off by default; opt in via the consumer's
// guardrail wiring when test-driven verification is what you want.
//
// Catches the failure mode observed on caddyserver/caddy-6115 in the
// Go baseline: the agent edited production code to a new behavior
// and "fixed" the test that was passing before. A test-running
// verifier sees the original test now fails (or a sibling test that
// covers the previous behavior) and reports the regression as a
// Validation error, giving the agent one more iteration to revert.
//
// Behaviour:
//
//   - Only runs on non-test files (paths NOT ending in _test.go).
//     Test-file edits are "tested" by virtue of the next test run
//     including them anyway, and running tests after every test edit
//     would double the cost.
//   - Per-package timeout configurable via Timeout (default 60s).
//     A package whose tests don't finish in time is reported as
//     timeout — soft signal, the agent decides whether to keep going.
//   - Returns the FIRST failing package's output, not an aggregate.
//     The agent reads diffable diagnostic text rather than a wall of
//     parallel-test noise.
type GoTestVerifier struct {
	// Bin overrides the go binary path. Empty falls back to "go".
	Bin string
	// Timeout caps each `go test` invocation. Zero defaults to 60s.
	Timeout time.Duration
}

// Name returns the verifier's identifier.
func (v *GoTestVerifier) Name() string { return "go_test_verifier" }

// Extensions handled — same as GoVerifier so the improvement
// guardrail can dispatch to both for the same edit.
func (v *GoTestVerifier) Extensions() []string { return []string{".go"} }

// Verify reduces paths to their containing packages (skipping test
// files), then runs `go test -count=1 -timeout=<T>` against each.
// The first failure short-circuits and returns the test output;
// passing packages are silent.
func (v *GoTestVerifier) Verify(ctx context.Context, root string, paths []string) error {
	bin := v.Bin
	if bin == "" {
		bin = "go"
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	pkgs := goTestPackagesOf(root, paths)
	if len(pkgs) == 0 {
		return nil
	}
	for _, pkg := range pkgs {
		arg := "./" + pkg
		if pkg == "" {
			arg = "./..."
		}
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(runCtx, bin, "test", "-count=1", fmt.Sprintf("-timeout=%s", timeout), arg)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			continue
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return fmt.Errorf("go test %s: %w", arg, err)
		}
		// Keep the diagnostic terse — the agent has limited attention.
		// Truncate to the first ~80 lines of output, which is usually
		// the failing test's stack + assertion.
		lines := strings.SplitN(trimmed, "\n", 100)
		if len(lines) > 80 {
			lines = lines[:80]
			lines = append(
				lines,
				fmt.Sprintf("... (output truncated; %d more lines)", len(strings.Split(trimmed, "\n"))-80),
			)
		}
		return fmt.Errorf("go test %s failed:\n%s", arg, strings.Join(lines, "\n"))
	}
	return nil
}

// goTestPackagesOf is like packagesOf (from go_verifier.go) but
// filters out _test.go files — we only run tests because PRODUCTION
// code changed, not because the agent edited a test file (which
// would be tested anyway by the next prod-code edit, and avoids the
// "edit test, run tests, see test passes vacuously" feedback loop).
func goTestPackagesOf(root string, paths []string) []string {
	var srcOnly []string
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		srcOnly = append(srcOnly, p)
	}
	return packagesOf(root, srcOnly)
}
