package engine

import (
	"context"
	"iter"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/zsync"
)

type completionEvidence struct {
	mu sync.Mutex

	sequence            uint64
	lastMutation        uint64
	lastVerification    uint64
	verificationPassed  bool
	verificationCommand string
	mutatedPaths        *zsync.Set[string]
}

type completionEvidenceSnapshot struct {
	LastMutation        uint64
	LastVerification    uint64
	VerificationPassed  bool
	VerificationCommand string
	MutatedPaths        []string
}

func newCompletionEvidence() *completionEvidence {
	return &completionEvidence{mutatedPaths: zsync.NewSet[string]()}
}
func (e *completionEvidence) record(call tools.ToolCall, result *tools.ToolResult, dispatchErr error) {
	if e == nil || dispatchErr != nil || result == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sequence++
	sequence := e.sequence

	if result.Success {
		for _, effect := range result.FileEffects() {
			switch effect.Op {
			case tools.FileCreate, tools.FileModify, tools.FileAppend, tools.FileDelete, tools.FileRename:
				e.lastMutation = sequence
				if effect.Path != "" {
					e.mutatedPaths.Add(filepath.ToSlash(filepath.Clean(effect.Path)))
				}
				if effect.FromPath != "" {
					e.mutatedPaths.Add(filepath.ToSlash(filepath.Clean(effect.FromPath)))
				}
			}
		}
	}

	if call.ToolName != code.ToolNameBash {
		return
	}
	command := strings.TrimSpace(call.Arguments.String("command", ""))
	if !isVerificationCommand(command) {
		return
	}
	for _, effect := range result.ProcessEffects() {
		if effect.Background {
			continue
		}
		e.lastVerification = sequence
		e.verificationPassed = result.Success && effect.ExitCode == 0 && !effect.TimedOut
		e.verificationCommand = command
		return
	}
}

func (e *completionEvidence) snapshot() completionEvidenceSnapshot {
	if e == nil {
		return completionEvidenceSnapshot{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	paths := zsync.Ordered(e.mutatedPaths)
	return completionEvidenceSnapshot{
		LastMutation:        e.lastMutation,
		LastVerification:    e.lastVerification,
		VerificationPassed:  e.verificationPassed,
		VerificationCommand: e.verificationCommand,
		MutatedPaths:        paths,
	}
}

func isVerificationCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) < 2 {
		return false
	}
	if fields[0] == "go" {
		switch fields[1] {
		case "test", "vet", "build":
			return true
		case "tool":
			return len(fields) >= 4 && fields[2] == "task" && (fields[3] == "check" || fields[3] == "lint" || fields[3] == "race")
		}
	}
	return fields[0] == "golangci-lint" && fields[1] == "run"
}

func hasVerifiableCode(paths []string) bool {
	for _, path := range paths {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".c", ".cc", ".cpp", ".h", ".hpp", ".java", ".js", ".jsx", ".py", ".rs", ".ts", ".tsx":
			return true
		}
	}
	return false
}

type evidenceSource struct {
	inner    tools.Source
	evidence *completionEvidence
}

func newEvidenceSource(inner tools.Source, evidence *completionEvidence) tools.Source {
	return &evidenceSource{inner: inner, evidence: evidence}
}

func (s *evidenceSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return s.inner.Tools(ctx)
}

func (s *evidenceSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	result, err := s.inner.Execute(ctx, call)
	s.evidence.record(call, result, err)
	return result, err
}

func (s *evidenceSource) ForgetTask(id taskscope.ID) {
	if forgetter, ok := s.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		forgetter.ForgetTask(id)
	}
}
