// Package evalconfig parses command-line configuration for the SWE-bench evaluator.
package evalconfig

import (
	"flag"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

// Config groups the evaluator's command-line settings by responsibility.
type Config struct {
	Input       InputConfig
	Zarlcode    ZarlcodeConfig
	Execution   ExecutionConfig
	Worktrees   WorktreeConfig
	Scoring     ScoringConfig
	Persistence PersistenceConfig
	Version     bool
}

// InputConfig selects tasks and evaluation arms.
type InputConfig struct {
	Tasks        string
	Drivers      string
	AblationSpec string
	Languages    string
	Sample       int
}

// LanguageFilter returns the configured comma-separated language filter.
func (c InputConfig) LanguageFilter() []string {
	if c.Languages == "" {
		return nil
	}
	return strings.Split(c.Languages, ",")
}

// Ablations resolves the configured ablation names in their requested order.
func (c InputConfig) Ablations() ([]harness.Ablation, error) {
	return harness.AblationArms(c.AblationSpec)
}

// ZarlcodeConfig configures the in-process zarlcode driver.
type ZarlcodeConfig struct {
	EnvFile             string
	Provider            string
	Model               string
	CodexEffort         string
	LlamacppResetURL    string
	AllowRemoteResetURL bool
	StateDB             string
	MaxIter             int
	ToolConcurrency     int
	ContextWindow       int
	StreamIdle          time.Duration
	IterationTimeout    time.Duration
	DeadlineGrace       time.Duration
	VerifiedAttempts    int
	VerifyWorkers       int
	VerifyWorkDir       string
	VerifyTimeout       time.Duration
	ThreadTranscript    bool
	TranscriptDir       string
}

// ExecutionConfig controls harness timing and parallelism.
type ExecutionConfig struct {
	TaskTimeout time.Duration
	Concurrency int
}

// WorktreeConfig controls task checkout materialization.
type WorktreeConfig struct {
	Dir        string
	CloneCache string
	Keep       bool
}

// ScoringConfig controls optional SWE-bench evaluator scoring.
type ScoringConfig struct {
	Enabled bool
	Dataset string
	Workers int
	WorkDir string
	Python  string
}

// PersistenceConfig controls the evaluation run record.
type PersistenceConfig struct {
	DBPath string
	RunID  string
	Notes  string
}

// Parse registers the eval command's flags on fs and parses args.
func Parse(fs *flag.FlagSet, args []string) (Config, error) {
	var cfg Config

	fs.StringVar(&cfg.Input.Tasks, "tasks", "", "path to SWE-bench JSONL or Parquet task file (required)")
	fs.StringVar(&cfg.Input.Drivers, "drivers", "zarlcode", "comma-separated list of drivers to run (zarlcode)")
	fs.StringVar(&cfg.Input.AblationSpec, "ablations", "", "comma-separated zarlcode guardrail-ablation arms, or \"all\" (baseline, no-shell, no-skill-hint, no-decompose, no-fanout, no-test-edit, no-improvement, judge); each arm runs as its own driver")
	fs.StringVar(&cfg.Input.Languages, "languages", "", "comma-separated language filter (empty = all)")
	fs.IntVar(&cfg.Input.Sample, "sample", 0, "stratified sample size (0 = run all matching specs)")
	fs.StringVar(&cfg.Zarlcode.EnvFile, "env", "", "path to .env loaded before provider construction (carries per-backend URL/key knobs)")
	fs.StringVar(&cfg.Zarlcode.Provider, "zarlcode-provider", "", "pin zarlcode's backend (registry name: llamacpp, openai-codex, gemini, claude-code, …); empty = registry default (llamacpp)")
	fs.StringVar(&cfg.Zarlcode.Model, "zarlcode-model", "", "pin zarlcode's model (e.g. gpt-5.5, qwen3.6-35b-a3b-mtp); empty = provider's default model")
	fs.StringVar(&cfg.Zarlcode.CodexEffort, "zarlcode-codex-effort", "", "codex_reasoning_effort when provider is openai-codex (low/medium/high/xhigh/max)")
	fs.StringVar(&cfg.Zarlcode.LlamacppResetURL, "llamacpp-reset-url", "", "POSTed before each task to flush local llama-server's KV cache slot; e.g. http://localhost:8081/slots/0?action=erase (requires --slot-save-path on the server)")
	fs.BoolVar(&cfg.Zarlcode.AllowRemoteResetURL, "allow-remote-reset-url", false, "permit a non-loopback --llamacpp-reset-url (default: loopback only, to avoid SSRF via a misconfigured URL)")
	fs.StringVar(&cfg.Zarlcode.StateDB, "state-db", "", "path to zarlcode state.db — vault + custom-provider rows (empty = $HOME/.zarlcode/state.db)")
	fs.DurationVar(&cfg.Execution.TaskTimeout, "task-timeout", 5*time.Minute, "wall-clock budget per (task, driver)")
	fs.IntVar(&cfg.Zarlcode.MaxIter, "max-iter", 0, "cap the agent loop's iterations (0 = loop default)")
	fs.IntVar(&cfg.Zarlcode.ToolConcurrency, "tool-concurrency", 0, "cap concurrent tool dispatch per iteration (0 = sequential)")
	fs.IntVar(&cfg.Zarlcode.ContextWindow, "context-window", 0, "compactor context-window size in tokens (0 = 32768)")
	fs.DurationVar(&cfg.Zarlcode.StreamIdle, "zarlcode-stream-idle", 0, "zarlcode stream-idle watchdog: gap between chunks before the stall detector fires (0 = coderunner default 90s); raise for slow-prefill local models")
	fs.DurationVar(&cfg.Zarlcode.IterationTimeout, "zarlcode-iteration-timeout", 0, "zarlcode per-iteration wall-clock backstop (0 = coderunner default 5m); raise for slow-prefill local models")
	fs.DurationVar(&cfg.Zarlcode.DeadlineGrace, "zarlcode-deadline-grace", 0, "time before the task deadline at which the wrap-up nudge fires (0 = disabled); give the model a last-chance to commit before the task-timeout cancels it")
	fs.IntVar(&cfg.Zarlcode.VerifiedAttempts, "zarlcode-verified-attempts", 0, "enable zarlcode harness re-drive with SWE-bench verifier; values >1 cap attempts, 0/1 = trust terminal reason")
	fs.IntVar(&cfg.Zarlcode.VerifyWorkers, "zarlcode-verify-workers", 1, "SWE-bench evaluator workers for per-attempt zarlcode verification")
	fs.StringVar(&cfg.Zarlcode.VerifyWorkDir, "zarlcode-verify-workdir", "", "directory for per-attempt zarlcode verification logs (empty = tempdir per attempt)")
	fs.DurationVar(&cfg.Zarlcode.VerifyTimeout, "zarlcode-verify-timeout", 0, "per-attempt SWE-bench verifier timeout, independent of the agent task timeout (0 = 30m)")
	fs.BoolVar(&cfg.Zarlcode.ThreadTranscript, "zarlcode-thread-transcript", false, "verified re-drives carry the full prior transcript (needs a large --context-window); default re-drives with verifier feedback only")
	fs.StringVar(&cfg.Zarlcode.TranscriptDir, "zarlcode-transcript-dir", "", "persist each task's full agent transcript to <dir>/<instance_id>.json for post-hoc debugging (empty = disabled)")
	fs.IntVar(&cfg.Execution.Concurrency, "concurrency", 1, "parallel (task, driver) invocations")
	fs.StringVar(&cfg.Worktrees.Dir, "worktree-dir", "", "where to materialize worktrees (empty = a fresh tempdir)")
	fs.StringVar(&cfg.Worktrees.CloneCache, "clone-cache", "", "optional --reference clone cache directory")
	fs.BoolVar(&cfg.Worktrees.Keep, "keep-worktrees", false, "leave worktrees on disk after the run for post-hoc inspection")
	fs.BoolVar(&cfg.Scoring.Enabled, "score", false, "after the harness loop, invoke SWE-bench's evaluator on each driver's diffs and report resolved/unresolved")
	fs.StringVar(&cfg.Scoring.Dataset, "score-dataset", "SWE-bench/SWE-bench_Multilingual", "dataset name passed to the SWE-bench evaluator")
	fs.IntVar(&cfg.Scoring.Workers, "score-workers", 4, "SWE-bench evaluator --max_workers")
	fs.StringVar(&cfg.Scoring.WorkDir, "score-workdir", "", "directory for the evaluator's predictions + logs (empty = a fresh tempdir)")
	fs.StringVar(&cfg.Scoring.Python, "score-python", "", "python interpreter that has the swebench package importable (empty = python3 on PATH; typical: a venv's bin/python)")
	fs.StringVar(&cfg.Persistence.DBPath, "db", "", "path to swebench-eval sqlite (empty = $HOME/.zarlcode/swebench-eval.db)")
	fs.StringVar(&cfg.Persistence.RunID, "run-id", "", "explicit run id (empty = a generated uuid)")
	fs.StringVar(&cfg.Persistence.Notes, "run-notes", "", "free-form notes to attach to the run row — eg. 'after decompose advisory refactor'")
	fs.BoolVar(&cfg.Version, "version", false, "print the build version and exit")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
