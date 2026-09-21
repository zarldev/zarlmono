package runner

import "time"

// ScoreAttempt records an evaluator invocation before launch and after settlement.
// A running record without a matching terminal record is unfinished, never success.
// Rebuild retries have distinct IDs; Summary retains the evaluator's original JSON.
type ScoreAttempt struct {
	ID              string     `json:"id"`
	Driver          string     `json:"driver"`
	RunID           string     `json:"run_id"`
	Dataset         string     `json:"dataset"`
	Python          string     `json:"python"`
	MaxWorkers      int        `json:"max_workers"`
	WorkDir         string     `json:"work_dir"`
	PredictionsPath string     `json:"predictions_path"`
	SummaryPath     string     `json:"summary_path"`
	Rebuild         bool       `json:"rebuild"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	// Status describes evaluator invocation completion, not whether scoring resolved tasks.
	Status     ScoreStatus `json:"status"`
	Diagnostic string      `json:"diagnostic"`
	Summary    string      `json:"summary"`
}
