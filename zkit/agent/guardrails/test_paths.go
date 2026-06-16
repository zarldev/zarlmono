package guardrails

import (
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// testEditTouchedPaths extracts the path args from common write-style tool calls.
// apply_patch operates on multiple paths inside its patch body, so parse its
// file headers and return every touched path.
func testEditTouchedPaths(call tools.ToolCall) []string {
	if call.ToolName == code.ToolNameApplyPatch {
		return code.PatchPaths(call.Arguments.String("patch", ""))
	}
	if p := call.Arguments.String("path", ""); p != "" {
		return []string{p}
	}
	return nil
}

// looksLikeTestFile recognises the common test-file conventions
// across the languages in SWE-bench Multilingual:
//
//	*_test.go    Go
//	*.test.{ts,tsx,js,jsx}   JS/TS
//	test_*.py    Python (not in Multilingual but cheap to add)
//	*_spec.rb / *_test.rb    Ruby
//	*Test.java   Java
//	test_*.{c,cpp}           C / C++
//	*_test.{c,cpp}
//	tests/...    catch-all directory check
//
// A path matching ANY pattern fires the advisory — false positives
// (a non-test file in a tests/ dir) are acceptable; the advisory is
// soft and the model decides.
func looksLikeTestFile(path string) bool {
	base := filepath.Base(path)
	lower := strings.ToLower(base)

	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"} {
		if strings.HasSuffix(lower, ".test"+ext) || strings.HasSuffix(lower, ".spec"+ext) {
			return true
		}
	}
	if strings.HasPrefix(lower, "test_") &&
		(strings.HasSuffix(lower, ".py") || strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".cpp")) {
		return true
	}
	if strings.HasSuffix(lower, "_spec.rb") || strings.HasSuffix(lower, "_test.rb") {
		return true
	}
	if strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java") {
		return true
	}
	if strings.HasSuffix(lower, "_test.c") || strings.HasSuffix(lower, "_test.cpp") {
		return true
	}
	if strings.HasSuffix(lower, "test.php") || strings.HasSuffix(lower, "_test.php") {
		return true
	}
	if strings.HasSuffix(lower, "_test.rs") {
		return true
	}
	// Directory-based catch: any segment named "test", "tests" or
	// "spec" along the path.
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		s := strings.ToLower(seg)
		if s == "test" || s == "tests" || s == "spec" || s == "specs" || s == "__tests__" {
			return true
		}
	}
	return false
}

// looksLikeTestPath is the broader sibling of [looksLikeTestFile]:
// it returns true for test FIXTURE / FIXTURE-DATA paths too, not
// just files named *_test.{go,py,…}. Used by the headless hard-
// reject path where touching ANY test-adjacent file is wrong, not
// just the test files themselves.
//
// Catches in addition to looksLikeTestFile:
//
//   - any segment named "testdata" (Go fixture convention)
//   - any segment named "fixtures" / "testfixtures" / "test-fixtures" / "test_fixtures"
//   - any segment named "snapshots" / "__snapshots__" (Jest et al)
//   - any segment named "golden" / "goldens" (golden-file convention)
//   - any segment ENDING with "test" (caddytest, integrationtest, …)
//   - file extensions .golden / .snap
//
// False positives we accept: a path under a directory whose name
// happens to end with "test" but isn't actually test infrastructure
// (e.g. an app called "loadtest"). The model gets a clear rejection
// message in those cases; consumer can configure around the heuristic
// if it ever bites.
func looksLikeTestPath(path string) bool {
	if looksLikeTestFile(path) {
		return true
	}
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".golden") || strings.HasSuffix(lower, ".snap") {
		return true
	}
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		s := strings.ToLower(seg)
		switch s {
		case "testdata", "fixtures", "testfixtures", "test-fixtures", "test_fixtures",
			"snapshots", "__snapshots__", "golden", "goldens":
			return true
		}
		// Segment ending with "test" — catches caddytest, integrationtest,
		// rendertest, etc. that house fixture / golden directories.
		// Length check excludes accidental matches on words like "latest"
		// or "attest" (those end with "test" too) — anything shorter
		// than 6 chars (longer than "test" + at-least-one prefix char)
		// is rejected to avoid those.
		if len(s) > 4 && strings.HasSuffix(s, "test") {
			// Whitelist common non-test words that end in "test"
			// before triggering. Bias toward false-positives is
			// acceptable per the doc above, but the few words that
			// appear in real codebases should pass.
			switch s {
			case "latest", "attest", "contest", "protest", "manifest":
				continue
			}
			return true
		}
	}
	return false
}

// annotateResult appends an advisory note to result.Data. Wraps the
// existing data when it's a string; for non-string data we stash the
// note in the result's Metadata so the runner's toolResultText can
// surface it (and human readers see it via log).
func annotateResult(result *tools.ToolResult, advisory string) {
	switch v := result.Data.(type) {
	case string:
		result.Data = v + "\n\n[" + advisory + "]"
	default:
		if result.Metadata == nil {
			result.Metadata = tools.ToolMetadata{}
		}
		result.Metadata["advisory"] = advisory
	}
}
