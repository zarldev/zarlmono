package engine

import (
	"cmp"
	"context"
	"iter"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/zsync"
)

const (
	maxOperationalFiles    = 32
	maxOperationalFailures = 8
	fileActionRead         = "read"
	fileActionEdit         = "edit"
)

type operationalState struct {
	filesMu sync.Mutex
	files   []compact.FileTouch
	tools   zsync.Map[tools.ToolName, *atomic.Int64]

	statusMu         sync.Mutex
	verification     *compact.VerificationState
	failures         []compact.FailureState
	sequence         uint64
	lastMutation     uint64
	lastVerification uint64
}

func newOperationalState() *operationalState {
	return &operationalState{}
}

func (s *operationalState) record(call tools.ToolCall, result *tools.ToolResult, dispatchErr error) {
	if s == nil {
		return
	}
	counter, _ := s.tools.LoadOrStore(call.ToolName, new(atomic.Int64))
	counter.Add(1)
	s.statusMu.Lock()
	s.sequence++
	sequence := s.sequence
	s.statusMu.Unlock()
	s.recordStatus(sequence, call, result, dispatchErr)
	if dispatchErr != nil || result == nil || !result.Success {
		return
	}
	for _, effect := range result.FileEffects() {
		action := fileTouchAction(effect.Op)
		if effect.Path == "" || action == "" {
			continue
		}
		s.statusMu.Lock()
		s.lastMutation = sequence
		s.statusMu.Unlock()
		s.recordFile(compact.FileTouch{Path: effect.Path, Action: action})
	}
	switch call.ToolName {
	case code.ToolNameRead:
		if path := call.Arguments.String("path", ""); path != "" {
			s.recordFile(compact.FileTouch{Path: path, Action: fileActionRead})
		}
	case code.ToolNameRetrieveCode:
		if retrieved, ok := result.Data.(code.RetrieveCodeResult); ok {
			for _, path := range retrieved.Paths() {
				s.recordFile(compact.FileTouch{Path: path, Action: fileActionRead})
			}
		}
	}
}

// ForgetTask forwards task lifecycle cleanup to the wrapped source. Operational
// state is session-wide and intentionally survives individual runner tasks.
func (s *operationalSource) ForgetTask(id taskscope.ID) {
	if forgetter, ok := s.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		forgetter.ForgetTask(id)
	}
}

func (s *operationalState) recordFile(touch compact.FileTouch) {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	if index := slices.IndexFunc(s.files, func(existing compact.FileTouch) bool {
		return existing.Path == touch.Path
	}); index >= 0 {
		s.files = slices.Delete(s.files, index, index+1)
	}
	s.files = append(s.files, touch)
	if len(s.files) > maxOperationalFiles {
		s.files = slices.Delete(s.files, 0, len(s.files)-maxOperationalFiles)
	}
}

func (s *operationalState) workingFiles() []compact.FileTouch {
	if s == nil {
		return nil
	}
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	return slices.Clone(s.files)
}

func (s *operationalState) topTools() []compact.ToolUsage {
	if s == nil {
		return nil
	}
	names := s.tools.Keys()
	usage := make([]compact.ToolUsage, 0, len(names))
	for _, name := range names {
		counter, err := s.tools.Get(name)
		if err != nil {
			continue
		}
		usage = append(usage, compact.ToolUsage{Name: name.String(), Count: int(counter.Load())})
	}
	slices.SortFunc(usage, func(a, b compact.ToolUsage) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return usage
}

func (s *operationalState) recordStatus(sequence uint64, call tools.ToolCall, result *tools.ToolResult, dispatchErr error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if call.ToolName == code.ToolNameBash && result != nil {
		command := strings.TrimSpace(call.Arguments.String("command", ""))
		if isVerificationCommand(command) {
			for _, effect := range result.ProcessEffects() {
				if effect.Background {
					continue
				}
				s.lastVerification = sequence
				s.verification = &compact.VerificationState{Command: command, Passed: result.Success && effect.ExitCode == 0 && !effect.TimedOut}
				if s.verification.Passed {
					s.failures = slices.DeleteFunc(s.failures, func(failure compact.FailureState) bool { return failure.Tool == code.ToolNameBash.String() })
				}
				break
			}
		}
	}
	if dispatchErr == nil && result != nil && result.Success {
		return
	}
	summary, kind := "tool dispatch failed", tools.Kinds.FATAL.String()
	if result != nil {
		summary = strings.TrimSpace(result.Error)
		if result.Err != nil {
			kind = result.Err.Kind.String()
		}
	} else if dispatchErr != nil {
		summary = dispatchErr.Error()
	}
	if summary == "" {
		summary = "tool reported failure"
	}
	failure := compact.FailureState{Tool: call.ToolName.String(), Kind: kind, Summary: clipFailureSummary(summary)}
	s.failures = slices.DeleteFunc(s.failures, func(existing compact.FailureState) bool { return existing.Tool == failure.Tool })
	s.failures = append(s.failures, failure)
	if len(s.failures) > maxOperationalFailures {
		s.failures = slices.Delete(s.failures, 0, len(s.failures)-maxOperationalFailures)
	}
}

func (s *operationalState) verificationState() *compact.VerificationState {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.verification == nil {
		return nil
	}
	state := *s.verification
	state.Stale = s.lastMutation > s.lastVerification
	return &state
}

func (s *operationalState) unresolvedFailures() []compact.FailureState {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return slices.Clone(s.failures)
}

func clipFailureSummary(summary string) string {
	const maxRunes = 240
	runes := []rune(summary)
	if len(runes) <= maxRunes {
		return summary
	}
	return string(runes[:maxRunes]) + "…"
}

func fileTouchAction(op tools.FileOp) string {
	switch op {
	case tools.FileRead:
		return fileActionRead
	case tools.FileCreate:
		return "create"
	case tools.FileModify:
		return fileActionEdit
	case tools.FileAppend:
		return "append"
	case tools.FileDelete:
		return "delete"
	case tools.FileRename:
		return "rename"
	default:
		return ""
	}
}

type operationalSource struct {
	inner tools.Source
	state *operationalState
}

func newOperationalSource(inner tools.Source, state *operationalState) tools.Source {
	return &operationalSource{inner: inner, state: state}
}

func (s *operationalSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return s.inner.Tools(ctx)
}

func (s *operationalSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	result, err := s.inner.Execute(ctx, call)
	s.state.record(call, result, err)
	return result, err
}
