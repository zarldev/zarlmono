package evalconfig_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

func parse(t *testing.T, args ...string) (evalconfig.Config, error) {
	t.Helper()
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	return evalconfig.Parse(fs, args)
}

func TestParseDefaults(t *testing.T) {
	cfg, err := parse(t)
	if err != nil {
		t.Fatalf("Parse defaults: %v", err)
	}
	if cfg.Input.Drivers != "zarlcode" || cfg.Execution.TaskTimeout != 5*time.Minute || cfg.Execution.Concurrency != 1 {
		t.Fatalf("core defaults = drivers %q, timeout %s, concurrency %d", cfg.Input.Drivers, cfg.Execution.TaskTimeout, cfg.Execution.Concurrency)
	}
	if cfg.Scoring.Dataset != "SWE-bench/SWE-bench_Multilingual" || cfg.Scoring.Workers != 4 {
		t.Fatalf("scoring defaults = dataset %q, workers %d", cfg.Scoring.Dataset, cfg.Scoring.Workers)
	}
	if cfg.Zarlcode.VerifyWorkers != 1 {
		t.Fatalf("verify workers default = %d, want 1", cfg.Zarlcode.VerifyWorkers)
	}
	if cfg.Input.Tasks != "" || cfg.Input.Sample != 0 || cfg.Version || cfg.Scoring.Enabled {
		t.Fatalf("zero-value defaults changed: %+v", cfg)
	}
}

func TestParseRepresentativeFullArgs(t *testing.T) {
	args := []string{
		"--tasks", "tasks.parquet", "--drivers", "zarlcode", "--ablations", "baseline,judge",
		"--languages", "go,rust", "--sample", "12", "--env", ".env",
		"--zarlcode-provider", "openai-codex", "--zarlcode-model", "gpt-5.5",
		"--zarlcode-codex-effort", "xhigh", "--llamacpp-reset-url", "http://localhost:8081/slots/0?action=erase",
		"--allow-remote-reset-url", "--state-db", "state.db", "--task-timeout", "17m",
		"--max-iter", "22", "--tool-concurrency", "3", "--context-window", "65536",
		"--zarlcode-stream-idle", "2m", "--zarlcode-iteration-timeout", "9m",
		"--zarlcode-deadline-grace", "45s", "--zarlcode-verified-attempts", "4",
		"--zarlcode-verify-workers", "5", "--zarlcode-verify-workdir", "verify",
		"--zarlcode-verify-timeout", "31m", "--zarlcode-thread-transcript",
		"--zarlcode-transcript-dir", "transcripts", "--concurrency", "6",
		"--worktree-dir", "worktrees", "--clone-cache", "clones", "--keep-worktrees",
		"--score", "--score-dataset", "dataset", "--score-workers", "7",
		"--score-workdir", "scores", "--score-python", "python", "--db", "eval.db",
		"--run-id", "run-1", "--run-notes", "notes", "--version",
	}
	cfg, err := parse(t, args...)
	if err != nil {
		t.Fatalf("Parse full args: %v", err)
	}

	want := evalconfig.Config{
		Input: evalconfig.InputConfig{
			Tasks: "tasks.parquet", Drivers: "zarlcode", AblationSpec: "baseline,judge",
			Languages: "go,rust", Sample: 12,
		},
		Zarlcode: evalconfig.ZarlcodeConfig{
			EnvFile: ".env", Provider: "openai-codex", Model: "gpt-5.5", CodexEffort: "xhigh",
			LlamacppResetURL: "http://localhost:8081/slots/0?action=erase", AllowRemoteResetURL: true,
			StateDB: "state.db", MaxIter: 22, ToolConcurrency: 3, ContextWindow: 65536,
			StreamIdle: 2 * time.Minute, IterationTimeout: 9 * time.Minute, DeadlineGrace: 45 * time.Second,
			VerifiedAttempts: 4, VerifyWorkers: 5, VerifyWorkDir: "verify", VerifyTimeout: 31 * time.Minute,
			ThreadTranscript: true, TranscriptDir: "transcripts",
		},
		Execution:   evalconfig.ExecutionConfig{TaskTimeout: 17 * time.Minute, Concurrency: 6},
		Worktrees:   evalconfig.WorktreeConfig{Dir: "worktrees", CloneCache: "clones", Keep: true},
		Scoring:     evalconfig.ScoringConfig{Enabled: true, Dataset: "dataset", Workers: 7, WorkDir: "scores", Python: "python"},
		Persistence: evalconfig.PersistenceConfig{DBPath: "eval.db", RunID: "run-1", Notes: "notes"},
		Version:     true,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("Parse full args =\n%+v\nwant:\n%+v", cfg, want)
	}
	if !reflect.DeepEqual(cfg.Input.LanguageFilter(), []string{"go", "rust"}) {
		t.Fatalf("language filter = %v", cfg.Input.LanguageFilter())
	}
}

func TestParseRejectsInvalidDurationAndCounts(t *testing.T) {
	for _, args := range [][]string{
		{"--task-timeout", "soon"},
		{"--zarlcode-stream-idle", "later"},
		{"--sample", "many"},
		{"--concurrency", "several"},
		{"--score-workers", "lots"},
	} {
		if _, err := parse(t, args...); err == nil {
			t.Errorf("Parse(%q) succeeded, want flag value error", args)
		}
	}
}

func TestAblationsRejectUnknownAndPreserveOrderAndLabels(t *testing.T) {
	allCfg, err := parse(t, "--ablations", "all")
	if err != nil {
		t.Fatalf("Parse all ablations: %v", err)
	}
	allArms, err := allCfg.Input.Ablations()
	if err != nil {
		t.Fatalf("All ablations: %v", err)
	}
	var allLabels []string
	for _, arm := range allArms {
		allLabels = append(allLabels, (&harness.ZarlcodeDriver{Ablation: arm}).Name())
	}
	wantAllLabels := []string{
		"zarlcode", "zarlcode-no-shell", "zarlcode-no-skill-hint", "zarlcode-no-decompose",
		"zarlcode-no-fanout", "zarlcode-no-test-edit", "zarlcode-no-improvement", "zarlcode-judge",
	}
	if !reflect.DeepEqual(allLabels, wantAllLabels) {
		t.Fatalf("all ablation labels = %v, want %v", allLabels, wantAllLabels)
	}

	cfg, err := parse(t, "--ablations", "judge,baseline,no-shell")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	arms, err := cfg.Input.Ablations()
	if err != nil {
		t.Fatalf("Ablations: %v", err)
	}
	var names, labels []string
	for _, arm := range arms {
		names = append(names, arm.Name)
		labels = append(labels, (&harness.ZarlcodeDriver{Ablation: arm}).Name())
	}
	if !reflect.DeepEqual(names, []string{"judge", "baseline", "no-shell"}) {
		t.Fatalf("ablation order = %v", names)
	}
	if !reflect.DeepEqual(labels, []string{"zarlcode-judge", "zarlcode", "zarlcode-no-shell"}) {
		t.Fatalf("driver labels = %v", labels)
	}

	cfg, err = parse(t, "--ablations", "unknown")
	if err != nil {
		t.Fatalf("Parse unknown ablation flag: %v", err)
	}
	if _, err := cfg.Input.Ablations(); err == nil || !strings.Contains(err.Error(), "unknown ablation arm") {
		t.Fatalf("Ablations unknown error = %v", err)
	}
}

func TestHelpSnapshot(t *testing.T) {
	want, err := os.ReadFile("testdata/help.golden")
	if err != nil {
		t.Fatalf("read help snapshot: %v", err)
	}
	var got bytes.Buffer
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(&got)
	_, err = evalconfig.Parse(fs, []string{"-help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("Parse -help error = %v, want flag.ErrHelp", err)
	}
	want = bytes.ReplaceAll(want, []byte(`\t`), []byte("\t"))
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("help output changed\n--- got ---\n%s\n--- want ---\n%s", got.Bytes(), want)
	}
}
