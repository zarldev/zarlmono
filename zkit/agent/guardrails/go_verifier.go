package guardrails

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GoVerifier runs `go vet` against the packages affected by a tool
// call. Vet parses, typechecks, and applies the standard analyzers
// in one pass — strictly faster than `go build` for the same coverage
// since it skips object-file emission, and it catches the failure
// modes that matter mid-turn (syntax, undefined identifiers, type
// mismatches, the common analyzer set).
//
// We deliberately don't run the test suite here. Full tests are
// expensive, and SWE-bench's evaluator runs them post-hoc anyway.
// The in-loop verifier exists to catch "your edit broke compilation"
// before the agent burns more turns on top of a broken tree.
type GoVerifier struct {
	// Bin overrides the go binary path. Empty falls back to "go" on
	// PATH. Tests set this to a stub when they want to exercise the
	// guardrail wiring without a real go install.
	Bin string
}

// Name returns the verifier's identifier.
func (v *GoVerifier) Name() string { return "go_verifier" }

// Extensions lists the suffixes GoVerifier handles. Only ".go".
func (v *GoVerifier) Extensions() []string { return []string{".go"} }

// Verify reduces paths to their containing packages (relative to
// root) and runs `go vet` against each. The first failing package
// short-circuits — one diagnostic is enough signal for the model
// to react.
//
// Errors include the package path and the trimmed vet output so the
// LLM has both location and detail. Empty package output (vet found
// nothing) with a non-zero exit is rare but surfaced verbatim.
func (v *GoVerifier) Verify(ctx context.Context, root string, paths []string) error {
	bin := v.Bin
	if bin == "" {
		bin = "go"
	}
	pkgs := packagesOf(root, paths)
	if len(pkgs) == 0 {
		return nil
	}
	for _, pkg := range pkgs {
		arg := "./" + pkg
		if pkg == "" {
			arg = "./..."
		}
		cmd := exec.CommandContext(ctx, bin, "vet", arg)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err == nil {
			continue
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return fmt.Errorf("go vet %s: %w", arg, err)
		}
		return fmt.Errorf("go vet %s:\n%s", arg, trimmed)
	}
	return nil
}

// packagesOf reduces a list of file paths to the unique set of
// package directories (as import paths relative to root) they
// belong to. Files outside root are silently dropped so a workspace-
// escape via symlink can't trick the verifier into vet'ing something
// the agent shouldn't see.
func packagesOf(root string, paths []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range paths {
		full := p
		if !filepath.IsAbs(p) {
			full = filepath.Join(root, p)
		}
		dir := filepath.Dir(full)
		rel, err := filepath.Rel(root, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	return out
}
