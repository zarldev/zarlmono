package harness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ErrEvidence identifies incomplete experimental evidence, including capture limits.
var ErrEvidence = errors.New("capture trial evidence")

const maxEvidenceBytes = 64 << 20

// TrialEvidence owns one private, bounded JSONL artifact. It implements canonical
// runner history capture; writes are synchronous and serialized. No grader fields
// or provider options/environment are serialized. Tool/model text remains untrusted
// and may itself contain sensitive information. The opener must call Close.
type TrialEvidence struct {
	mu      sync.Mutex
	digest  hash.Hash
	receipt EvidenceReceipt
	file    *os.File
	bytes   int
	err     error
	closed  bool
}

var _ runner.HistorySink = (*TrialEvidence)(nil)

// OpenTrialEvidence creates a unique artifact beneath parent and records only the
// task's agent-visible input and trial identity. It never uses task IDs as paths.
func OpenTrialEvidence(parent, runID string, trial Trial, task Task) (*TrialEvidence, error) {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create directory: %w", ErrEvidence, err)
	}
	f, err := os.CreateTemp(parent, "trial-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("%w: create file: %w", ErrEvidence, err)
	}
	e := &TrialEvidence{file: f, digest: sha256.New(), receipt: EvidenceReceipt{
		File: filepath.Base(f.Name()), RunID: runID, TaskID: task.ID, Driver: trial.DriverName(),
	}}
	start := struct {
		Version        int          `json:"version"`
		RunID          string       `json:"run_id"`
		TaskID         string       `json:"task_id"`
		BaseCommit     string       `json:"base_commit"`
		Language       string       `json:"language"`
		Prompt         evidenceText `json:"task_prompt"`
		Trial          Trial        `json:"trial"`
		PromptRevision string       `json:"evaluation_prompt_revision"`
	}{1, runID, task.ID, task.BaseCommit, task.Language, captureText(task.Prompt()), trial, EvaluationPromptRevision}
	if err := e.record("start", start); err != nil {
		return nil, errors.Join(err, e.Close())
	}
	return e, nil
}

// Append persists canonical occurrences before compaction can remove them. It
// also records terminal observations after cancellation; no context is retained.
func (e *TrialEvidence) Append(_ context.Context, records []runner.ReplayMessage) error {
	return e.record("history", evidenceHistory(records))
}

// Request records the actual model-facing prompt/tool surface, excluding opaque
// provider options. Each request remains distinct from canonical history.
func (e *TrialEvidence) Request(_ context.Context, req llm.CompletionRequest) error {
	return e.record("request", evidenceRequest{llm.CompletionRequest{
		Messages: req.Messages, Tools: req.Tools, Temperature: req.Temperature,
		MaxTokens: req.MaxTokens, Stream: req.Stream, Thinking: req.Thinking,
		ResponseFormat: req.ResponseFormat, ChatTemplateKwargs: req.ChatTemplateKwargs,
	}})
}

// Finish records a score-blind result and closes the artifact. A missing result
// record or a returned error means incomplete evidence, not a successful trial.
// Official verification/scoring fields and raw subprocess logs are excluded.
func (e *TrialEvidence) Finish(result Result) error {
	diagnostic := ""
	if result.Err != nil {
		diagnostic = result.Err.Error()
	}
	err := e.record("result", struct {
		Diff           evidenceText   `json:"diff"`
		DurationMS     int64          `json:"duration_ms"`
		Iterations     int            `json:"iterations"`
		ToolCalls      int            `json:"tool_calls"`
		TokensIn       int64          `json:"tokens_in"`
		TokensOut      int64          `json:"tokens_out"`
		Provider       string         `json:"provider"`
		Model          string         `json:"model"`
		TerminalReason string         `json:"terminal_reason"`
		Rejections     map[string]int `json:"guardrail_rejections"`
		Error          evidenceText   `json:"error"`
	}{captureText(result.Diff), result.Duration.Milliseconds(), result.Iterations, result.ToolCalls,
		result.TokensIn, result.TokensOut, result.Provider, result.Model, result.TerminalReason,
		result.GuardrailRejections, captureText(diagnostic)})
	return errors.Join(err, e.Close())
}

// Close syncs and closes the artifact after its runner has settled. It is
// idempotent and returns any sticky capture/sync/close error on every call.
func (e *TrialEvidence) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		if err := errors.Join(e.file.Sync(), e.file.Close()); err != nil {
			e.err = errors.Join(e.err, fmt.Errorf("%w: close %s: %w", ErrEvidence, filepath.Base(e.file.Name()), err))
		}
	}
	return e.err
}

func (e *TrialEvidence) record(kind string, value any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	if e.closed {
		return fmt.Errorf("%w: %w", ErrEvidence, os.ErrClosed)
	}
	data, err := json.Marshal(struct {
		Kind string `json:"kind"`
		Data any    `json:"data"`
	}{kind, value})
	if err == nil && len(data)+1 > maxEvidenceBytes-e.bytes {
		err = errors.New("64 MiB evidence limit exceeded")
	}
	if err == nil {
		data = append(data, '\n')
		var n int
		n, err = e.file.Write(data)
		_, _ = e.digest.Write(data[:n])
		e.bytes += len(data)
	}
	if err != nil {
		e.err = fmt.Errorf("%w: %s: %w", ErrEvidence, filepath.Base(e.file.Name()), err)
	}
	return e.err
}

// These wrappers use the runner's byte-preserving storage format, not provider JSON.
type evidenceHistory []runner.ReplayMessage

func (records evidenceHistory) MarshalJSON() ([]byte, error) {
	data := make([]json.RawMessage, len(records))
	for i, record := range records {
		encoded, err := runner.MarshalReplayMessage(record)
		if err != nil {
			return nil, err
		}
		data[i] = encoded
	}
	return json.Marshal(data)
}

type evidenceRequest struct{ request llm.CompletionRequest }

func (r evidenceRequest) MarshalJSON() ([]byte, error) {
	return runner.MarshalHistoryRequest(r.request)
}

// Artifact text is readable UTF-8 when possible, otherwise lossless base64.
// An empty object denotes empty text; bytes_base64 takes precedence when present.
type evidenceText struct {
	Text  string `json:"text,omitempty"`
	Bytes []byte `json:"bytes_base64,omitempty"`
}

func captureText(value string) evidenceText {
	if utf8.ValidString(value) {
		return evidenceText{Text: value}
	}
	return evidenceText{Bytes: []byte(value)}
}
