package runner

import (
	"os"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

// TaskResult is one row in the result set: the outcome of one driver
// running one task. The triple (InstanceID, DriverName, Language) is
// unique per evaluation run; the comparison report groups on
// DriverName to produce per-harness summaries.
//
// Resolved + EvaluatorError populate only after Score has run against
// the Results — nil pointer = scoring hasn't run (or this record was
// skipped because of a driver-level error).
type TaskResult struct {
	InstanceID     string
	DriverName     string
	Language       string
	WorktreePath   string
	Result         harness.Result
	Resolved       *bool
	EvaluatorError string
}

// Results is the full set of TaskResults from one evaluation run plus
// timing bookends. The report package consumes this directly.
type Results struct {
	Started time.Time
	Ended   time.Time
	Records []TaskResult
}

// Duration returns the wall-clock span of the evaluation.
func (r Results) Duration() time.Duration { return r.Ended.Sub(r.Started) }

// removeAll is a thin wrapper around os.RemoveAll so the runner's
// happy-path doesn't need to import os directly.
func removeAll(path string) error { return os.RemoveAll(path) }
