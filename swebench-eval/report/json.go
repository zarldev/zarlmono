package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/db"
)

// ErrInvalidMetadata means saved JSON cannot be represented faithfully in an export.
var ErrInvalidMetadata = errors.New("invalid saved evaluation metadata")

type exportRun struct {
	ID             string     `json:"id"`
	StartedAt      time.Time  `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at"`
	DatasetName    string     `json:"dataset_name"`
	LanguageFilter string     `json:"language_filter"`
	SampleSize     int        `json:"sample_size"`
	Drivers        string     `json:"drivers"`
	TaskTimeoutMs  int64      `json:"task_timeout_ms"`
	Notes          string     `json:"notes"`
	ScoreStatus    string     `json:"score_status"`
	ScoreError     string     `json:"score_error"`
}

type exportResult struct {
	InstanceID          string          `json:"instance_id"`
	DriverName          string          `json:"driver_name"`
	Language            string          `json:"language"`
	Provider            string          `json:"provider"`
	Model               string          `json:"model"`
	WorktreePath        string          `json:"worktree_path"`
	Diff                string          `json:"diff"`
	DurationMs          int64           `json:"duration_ms"`
	Iterations          int             `json:"iterations"`
	ToolCalls           int             `json:"tool_calls"`
	Usage               *exportUsage    `json:"usage"`
	TerminalReason      string          `json:"terminal_reason"`
	Error               string          `json:"error"`
	Resolved            *bool           `json:"resolved"`
	EvaluatorError      string          `json:"evaluator_error"`
	GuardrailRejections json.RawMessage `json:"guardrail_rejections"`
	Verified            bool            `json:"verified"`
	Attempts            int             `json:"attempts"`
	AttemptVerdicts     json.RawMessage `json:"attempt_verdicts"`
	CreatedAt           time.Time       `json:"created_at"`
}

type exportUsage struct {
	TokensIn  int64 `json:"tokens_in"`
	TokensOut int64 `json:"tokens_out"`
}

type exportScoreAttempt struct {
	Sequence   int64           `json:"sequence"`
	AttemptID  string          `json:"attempt_id"`
	Status     string          `json:"status"`
	RecordedAt time.Time       `json:"recorded_at"`
	Payload    json.RawMessage `json:"payload"`
}

// JSON writes a versioned artifact from a transactionally consistent run snapshot.
// Unknown manifests and absent verdicts remain null. All-zero legacy token counts
// become null usage because storage does not distinguish unreported usage from zero.
// Saved metadata is validated before any output; malformed JSON wraps ErrInvalidMetadata.
func JSON(w io.Writer, snapshot db.RunSnapshot) error {
	manifest, err := savedJSON(snapshot.Run.ManifestJSON)
	if err != nil {
		return fmt.Errorf("run manifest: %w", err)
	}
	results := make([]exportResult, 0, len(snapshot.Results))
	for _, row := range snapshot.Results {
		result, err := resultJSON(row)
		if err != nil {
			return err
		}
		results = append(results, result)
	}
	attempts := make([]exportScoreAttempt, 0, len(snapshot.ScoreAttempts))
	for _, row := range snapshot.ScoreAttempts {
		payload, err := savedJSON(row.Payload)
		if err != nil {
			return fmt.Errorf("scoring attempt: %w", err)
		}
		attempts = append(attempts, exportScoreAttempt{
			Sequence: row.Sequence, AttemptID: row.AttemptID, Status: row.Status,
			RecordedAt: row.RecordedAt.UTC(), Payload: payload,
		})
	}
	r := snapshot.Run
	var endedAt *time.Time
	if r.EndedAt != nil {
		endedAt = new(r.EndedAt.UTC())
	}
	artifact := struct {
		FormatVersion int                  `json:"format_version"`
		Run           exportRun            `json:"run"`
		Manifest      json.RawMessage      `json:"manifest"`
		Results       []exportResult       `json:"results"`
		ScoreAttempts []exportScoreAttempt `json:"score_attempts"`
	}{
		FormatVersion: 1,
		Run: exportRun{
			ID: r.ID, StartedAt: r.StartedAt.UTC(), EndedAt: endedAt, DatasetName: r.DatasetName,
			LanguageFilter: r.LanguageFilter, SampleSize: r.SampleSize, Drivers: r.Drivers,
			TaskTimeoutMs: r.TaskTimeoutMs, Notes: r.Notes, ScoreStatus: r.ScoreStatus, ScoreError: r.ScoreError,
		},
		Manifest: manifest, Results: results, ScoreAttempts: attempts,
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(artifact); err != nil {
		return fmt.Errorf("write evaluation JSON: %w", err)
	}
	return nil
}

func resultJSON(r db.ResultRecord) (exportResult, error) {
	guards, err := savedJSON(r.GuardrailRejections)
	if err != nil {
		return exportResult{}, fmt.Errorf("guardrail counts: %w", err)
	}
	verdicts, err := savedJSON(r.AttemptVerdicts)
	if err != nil {
		return exportResult{}, fmt.Errorf("attempt verdicts: %w", err)
	}
	var usage *exportUsage
	if r.TokensIn != 0 || r.TokensOut != 0 {
		usage = &exportUsage{TokensIn: r.TokensIn, TokensOut: r.TokensOut}
	}
	return exportResult{
		InstanceID: r.InstanceID, DriverName: r.DriverName, Language: r.Language,
		Provider: r.Provider, Model: r.Model, WorktreePath: r.WorktreePath, Diff: r.Diff,
		DurationMs: r.DurationMs, Iterations: r.Iterations, ToolCalls: r.ToolCalls, Usage: usage,
		TerminalReason: r.TerminalReason, Error: r.Error, Resolved: r.Resolved, EvaluatorError: r.EvaluatorError,
		GuardrailRejections: guards, Verified: r.Verified, Attempts: r.Attempts,
		AttemptVerdicts: verdicts, CreatedAt: r.CreatedAt.UTC(),
	}, nil
}

func savedJSON(raw string) (json.RawMessage, error) {
	if raw == "" {
		return nil, nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, ErrInvalidMetadata
	}
	return json.RawMessage(raw), nil
}
