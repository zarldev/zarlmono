package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// EvidenceReceipt identifies the exact bytes of a settled artifact, not a score.
// File is a basename beneath the run's host-owned evidence directory.
type EvidenceReceipt struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	RunID  string `json:"run_id"`
	TaskID string `json:"task_id"`
	Driver string `json:"driver"`
}

// ToolObservation is a metadata-only, citation-addressable execution observation.
// Arguments, output, error prose, reasoning, and provider continuation are absent.
type ToolObservation struct {
	ID         string `json:"id"`
	Tool       string `json:"tool"`
	Success    bool   `json:"success"`
	Dispatched *bool  `json:"dispatched"`
	ErrorKind  string `json:"error_kind"`
}

// EvidenceView is the deliberately reduced evidence available to an improver.
// TaskPrompt and Guidance remain untrusted text, not authority to execute actions.
type EvidenceView struct {
	ID             string            `json:"id"`
	TaskPrompt     string            `json:"task_prompt"`
	Guidance       string            `json:"guidance"`
	PromptRevision string            `json:"prompt_revision"`
	Observations   []ToolObservation `json:"observations"`
	TerminalReason string            `json:"terminal_reason"`
	TokensIn       int64             `json:"tokens_in"`
	TokensOut      int64             `json:"tokens_out"`
}

// ReadTrialEvidence verifies a receipt before projecting metadata from a bounded
// artifact. It rejects incomplete/cancelled/errored trials and limits projections
// to 256 executions. os.Root confines file and symlink resolution to directory.
func ReadTrialEvidence(ctx context.Context, directory string, receipt EvidenceReceipt) (EvidenceView, error) {
	if err := ctx.Err(); err != nil {
		return EvidenceView{}, err
	}
	if receipt.File == "." || !filepath.IsLocal(receipt.File) || filepath.Base(receipt.File) != receipt.File || len(receipt.SHA256) != 64 {
		return EvidenceView{}, fmt.Errorf("%w: invalid receipt", ErrEvidence)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return EvidenceView{}, fmt.Errorf("%w: open directory: %w", ErrEvidence, err)
	}
	defer root.Close()
	file, err := root.Open(receipt.File)
	if err != nil {
		return EvidenceView{}, fmt.Errorf("%w: open artifact: %w", ErrEvidence, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxEvidenceBytes+1))
	if err != nil {
		return EvidenceView{}, fmt.Errorf("%w: read: %w", ErrEvidence, err)
	}
	if len(data) > maxEvidenceBytes || fmt.Sprintf("%x", sha256.Sum256(data)) != receipt.SHA256 {
		return EvidenceView{}, fmt.Errorf("%w: artifact size or digest mismatch", ErrEvidence)
	}
	view, err := projectEvidence(ctx, data, receipt)
	if err != nil {
		return EvidenceView{}, fmt.Errorf("%w: %w", ErrEvidence, err)
	}
	return view, nil
}

func projectEvidence(ctx context.Context, data []byte, receipt EvidenceReceipt) (EvidenceView, error) {
	view := EvidenceView{ID: receipt.SHA256}
	decoder := json.NewDecoder(bytes.NewReader(data))
	started, finished := false, false
	advertised := map[string]bool{}
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return EvidenceView{}, err
		}
		var event struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return EvidenceView{}, err
		}
		if finished || (!started && event.Kind != "start") {
			return EvidenceView{}, errors.New("invalid evidence order")
		}
		switch event.Kind {
		case "start":
			if started {
				return EvidenceView{}, errors.New("duplicate start")
			}
			var start struct {
				Version        int          `json:"version"`
				RunID          string       `json:"run_id"`
				TaskID         string       `json:"task_id"`
				Prompt         evidenceText `json:"task_prompt"`
				PromptRevision string       `json:"evaluation_prompt_revision"`
				Trial          struct {
					Driver   string `json:"driver"`
					Guidance string `json:"guidance"`
				} `json:"trial"`
			}
			if err := json.Unmarshal(event.Data, &start); err != nil {
				return EvidenceView{}, err
			}
			if start.Version != 1 || start.RunID != receipt.RunID || start.TaskID != receipt.TaskID || start.Trial.Driver != receipt.Driver || start.PromptRevision != EvaluationPromptRevision {
				return EvidenceView{}, errors.New("unsupported or mismatched evidence identity/prompt revision")
			}
			if len(start.Prompt.Bytes) != 0 || !utf8.ValidString(start.Prompt.Text) || len(start.Prompt.Text) > 32<<10 || len(start.Trial.Guidance) > 16<<10 {
				return EvidenceView{}, errors.New("task or guidance is not bounded UTF-8 text")
			}
			view.TaskPrompt, view.Guidance, view.PromptRevision = start.Prompt.Text, start.Trial.Guidance, start.PromptRevision
			started = true
		case "request":
			req, err := runner.UnmarshalHistoryRequest(event.Data)
			if err != nil {
				return EvidenceView{}, err
			}
			for _, tool := range req.Tools {
				advertised[tool.Function.Name] = true
			}
		case "history":
			var messages []json.RawMessage
			if err := json.Unmarshal(event.Data, &messages); err != nil {
				return EvidenceView{}, err
			}
			for _, raw := range messages {
				message, err := runner.UnmarshalReplayMessage(raw)
				if err != nil {
					return EvidenceView{}, err
				}
				tool := message.Tool
				if tool == nil {
					continue
				}
				if tool.ExecutionID == "" {
					return EvidenceView{}, errors.New("execution identity missing")
				}
				if seen[tool.ExecutionID] {
					continue
				}
				seen[tool.ExecutionID] = true
				if len(view.Observations) >= 256 {
					return EvidenceView{}, errors.New("execution projection limit exceeded")
				}
				name := "unadvertised"
				if advertised[tool.ToolName] {
					name = tool.ToolName
				}
				observation := ToolObservation{ID: fmt.Sprintf("%s/tool/%d", view.ID, len(view.Observations)+1), Tool: name, Success: tool.Success, ErrorKind: tool.Kind.String()}
				if tool.Dispatched != nil {
					observation.Dispatched = new(*tool.Dispatched)
				}
				view.Observations = append(view.Observations, observation)
			}
		case "result":
			var result struct {
				Reason    string       `json:"terminal_reason"`
				Error     evidenceText `json:"error"`
				TokensIn  int64        `json:"tokens_in"`
				TokensOut int64        `json:"tokens_out"`
			}
			if err := json.Unmarshal(event.Data, &result); err != nil {
				return EvidenceView{}, err
			}
			if (result.Reason != "completed" && result.Reason != "max_iterations") || result.Error.Text != "" || len(result.Error.Bytes) != 0 {
				return EvidenceView{}, errors.New("trial did not produce usable completed evidence")
			}
			view.TerminalReason, view.TokensIn, view.TokensOut = result.Reason, result.TokensIn, result.TokensOut
			finished = true
		default:
			return EvidenceView{}, errors.New("unsupported evidence record")
		}
	}
	if !started || !finished {
		return EvidenceView{}, errors.New("incomplete evidence")
	}
	return view, nil
}
