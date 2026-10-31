package evalconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

func TestExperimentConfigSnapshotsAndRejectsMixedPolicies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "experiment.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"repeats":2,"candidates":[{"label":"navigation","guidance":"Read the package first."}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--harness-experiment", path, "--harness-evidence-dir", filepath.Join(dir, "evidence"), "--zarlcode-provider", "fixture", "--zarlcode-model", "fixed"}
	cfg, err := parse(t, args...)
	if err != nil || len(cfg.Experiment.Trials()) != 4 {
		t.Fatalf("parse experiment: %v", err)
	}
	for name, extra := range map[string][]string{
		"ablation":         {"--ablations", "baseline"},
		"verified":         {"--zarlcode-verified-attempts", "2"},
		"threaded":         {"--zarlcode-thread-transcript"},
		"duplicate driver": {"--drivers", "zarlcode,zarlcode"},
		"unknown driver":   {"--drivers", "other"},
		"model default":    {"--zarlcode-model", ""},
		"provider default": {"--zarlcode-provider", ""},
		"no evidence":      {"--harness-evidence-dir", ""},
		"unbounded":        {"--task-timeout", "0"},
		"shared slot":      {"--llamacpp-reset-url", "http://localhost/slot", "--concurrency", "2"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, append(append([]string{}, args...), extra...)...)
			if !errors.Is(err, harness.ErrExperiment) {
				t.Fatalf("error = %v, want ErrExperiment", err)
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Experiment.Trials()) != 4 {
		t.Fatal("experiment reread a deleted input")
	}
	if _, err := parse(t, "--harness-evidence-dir", dir); !errors.Is(err, harness.ErrExperiment) {
		t.Fatalf("unused evidence flag: %v", err)
	}
	// Inspection-only commands must not load experiment files or require providers.
	for _, mode := range [][]string{{"--export-run", "existing"}, {"--version"}} {
		if _, err := parse(t, append(mode, "--harness-experiment", path)...); err != nil {
			t.Fatal(err)
		}
	}
}
