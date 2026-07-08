package guardrails

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// ReadBeforeWriteMode controls how the read-before-write guardrail reacts when
// a mutating file-tool call lands before the task has established local code
// context.
type ReadBeforeWriteMode int

const (
	ReadBeforeWriteOff ReadBeforeWriteMode = iota
	ReadBeforeWriteAdvisory
	ReadBeforeWriteStrict

	readBeforeWriteVerbWrite = "write"
)

// ReadBeforeWriteGuardrail refuses blind write/edit calls unless the task has
// already established enough local context via successful pure read/search
// calls recorded in the shared task ledger.
type ReadBeforeWriteGuardrail struct {
	ledger TaskCallLedger
	mode   ReadBeforeWriteMode
}

// TaskCallLedger is the narrow read-evidence seam the guardrail needs.
type TaskCallLedger interface {
	Calls(ctx context.Context) []runner.ObservedCall
}

// NewReadBeforeWriteGuardrail builds a read-before-write guardrail backed by
// ledger. Off mode returns nil so callers can append it directly without extra
// branching if they want.
func NewReadBeforeWriteGuardrail(ledger TaskCallLedger, mode ReadBeforeWriteMode) *ReadBeforeWriteGuardrail {
	if ledger == nil || mode == ReadBeforeWriteOff {
		return nil
	}
	return &ReadBeforeWriteGuardrail{ledger: ledger, mode: mode}
}

// Name returns the guardrail identifier.
func (g *ReadBeforeWriteGuardrail) Name() string { return "read_before_write" }

// Before rejects or nudges edit/write calls that have not first established
// enough local file context in the current task.
func (g *ReadBeforeWriteGuardrail) Before(ctx context.Context, call tools.ToolCall) error {
	if g == nil || g.mode == ReadBeforeWriteOff {
		return nil
	}
	if call.ToolName != code.ToolNameEdit && call.ToolName != code.ToolNameWrite {
		return nil
	}
	path := normalizeEvidencePath(call.Arguments.String("path", ""))
	if path == "" {
		return nil
	}
	calls := g.ledger.Calls(ctx)
	if call.ToolName == code.ToolNameWrite && hasCreationEvidence(path, calls) {
		return nil
	}
	if hasSufficientContext(call.ToolName, path, calls) {
		return nil
	}
	return tools.Validation("read_before_write", readBeforeWriteReason(call.ToolName, path, g.mode))
}

func hasSufficientContext(tool tools.ToolName, path string, calls []runner.ObservedCall) bool {
	if hasReadPath(path, calls) || hasWritePath(path, calls) || hasTestPairRead(path, calls) {
		return true
	}
	dir := filepath.Dir(path)
	if tool == code.ToolNameWrite {
		if hasDirListing(dir, calls) || hasReadInDir(dir, calls) {
			return true
		}
	}
	return hasReadInDir(dir, calls) && hasSearchEvidence(calls)
}

func hasCreationEvidence(path string, calls []runner.ObservedCall) bool {
	dir := filepath.Dir(path)
	if hasDirListing(dir, calls) || hasReadInDir(dir, calls) {
		return true
	}
	for _, call := range calls {
		if call.ToolName != code.ToolNameGlob {
			continue
		}
		pattern := normalizeEvidencePath(call.Arguments.String("pattern", ""))
		if pattern == "" {
			continue
		}
		if globCouldCoverPath(pattern, path) || filepath.Dir(pattern) == dir {
			return true
		}
	}
	return false
}

func globCouldCoverPath(pattern, path string) bool {
	if pattern == path {
		return true
	}
	if strings.HasPrefix(pattern, "**/") {
		if ok, _ := filepath.Match(strings.TrimPrefix(pattern, "**/"), filepath.Base(path)); ok {
			return true
		}
	}
	if strings.HasSuffix(pattern, "*") && strings.HasPrefix(path, strings.TrimSuffix(pattern, "*")) {
		return true
	}
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	return false
}

func hasReadPath(path string, calls []runner.ObservedCall) bool {
	for _, call := range calls {
		if call.ToolName != code.ToolNameRead {
			continue
		}
		if normalizeEvidencePath(call.Arguments.String("path", "")) == path {
			return true
		}
	}
	return false
}

// hasWritePath reports whether the task already mutated path via a successful
// edit/write. Having produced the file's current contents is itself established
// context, so a follow-up edit to the same path must not trip the
// read-before-write nudge (otherwise a normal edit → edit sequence degenerates
// into read → edit → read → edit).
func hasWritePath(path string, calls []runner.ObservedCall) bool {
	for _, call := range calls {
		if call.ToolName != code.ToolNameEdit && call.ToolName != code.ToolNameWrite {
			continue
		}
		if normalizeEvidencePath(call.Arguments.String("path", "")) == path {
			return true
		}
	}
	return false
}

func hasReadInDir(dir string, calls []runner.ObservedCall) bool {
	for _, call := range calls {
		if call.ToolName != code.ToolNameRead {
			continue
		}
		p := normalizeEvidencePath(call.Arguments.String("path", ""))
		if p != "" && filepath.Dir(p) == dir {
			return true
		}
	}
	return false
}

func hasDirListing(dir string, calls []runner.ObservedCall) bool {
	for _, call := range calls {
		if call.ToolName != code.ToolNameLs {
			continue
		}
		p := normalizeEvidencePath(call.Arguments.String("path", ""))
		if p == dir || (p == "" && dir == ".") {
			return true
		}
	}
	return false
}

func hasSearchEvidence(calls []runner.ObservedCall) bool {
	for _, call := range calls {
		if call.ToolName == code.ToolNameGrep || call.ToolName == code.ToolNameGlob {
			return true
		}
	}
	return false
}

func hasTestPairRead(path string, calls []runner.ObservedCall) bool {
	pair := testPairPath(path)
	if pair == "" {
		return false
	}
	return hasReadPath(pair, calls)
}

func testPairPath(path string) string {
	base := filepath.Base(path)
	dir := filepath.Dir(path)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return filepath.ToSlash(filepath.Join(dir, strings.TrimSuffix(base, "_test.go")+".go"))
	case strings.HasSuffix(base, ".go"):
		name := strings.TrimSuffix(base, ".go")
		if strings.HasSuffix(name, "_test") {
			return ""
		}
		return filepath.ToSlash(filepath.Join(dir, name+"_test.go"))
	default:
		return ""
	}
}

func normalizeEvidencePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." {
		return "."
	}
	return strings.TrimPrefix(clean, "./")
}

func readBeforeWriteReason(tool tools.ToolName, path string, mode ReadBeforeWriteMode) string {
	verb := "modify"
	if tool == code.ToolNameWrite {
		verb = readBeforeWriteVerbWrite
	}
	prefix := ""
	if mode == ReadBeforeWriteAdvisory {
		prefix = "advisory: "
	}
	return fmt.Sprintf(
		"%syou are about to %s %q without first reading that file or establishing enough nearby context in this task. "+
			"For an existing file, call read(path=%q, ...) and use edit with the returned anchors. "+
			"For a new file, first establish nearby context with ls/glob/read in the parent directory, then call write — do not fall back to bash/python just to create files.",
		prefix, verb, path, path,
	)
}
