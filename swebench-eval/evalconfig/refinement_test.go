package evalconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	statedb "github.com/zarldev/zarlmono/zkit/db"
)

func refinementArgs(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	return []string{"--harness-refine", "--sample", "1", "--max-iter", "8", "--zarlcode-provider", "fixture", "--zarlcode-model", "fixed", "--harness-evidence-dir", filepath.Join(dir, "evidence"), "--state-db", filepath.Join(dir, "isolated.db")}
}

func TestRefinementRequiresExplicitBoundedConfiguration(t *testing.T) {
	args := refinementArgs(t)
	cfg, err := parse(t, args...)
	if err != nil || !cfg.Refinement.Enabled {
		t.Fatalf("refinement config: %+v %v", cfg, err)
	}
	for name, flags := range map[string][]string{
		"static experiment": {"--harness-experiment", "missing.json"}, "ablation": {"--ablations", "baseline"},
		"redrive": {"--zarlcode-verified-attempts", "2"}, "threading": {"--zarlcode-thread-transcript"},
		"parallel tasks": {"--concurrency", "2"}, "unbounded iterations": {"--max-iter", "0"}, "too many iterations": {"--max-iter", "65"},
		"unbounded tasks": {"--sample", "0"}, "too many tasks": {"--sample", "11"},
		"unbounded duration": {"--task-timeout", "0"}, "long task": {"--task-timeout", "11m"},
		"long improver": {"--harness-improver-timeout", "3m"}, "zero improver": {"--harness-improver-timeout", "0"},
		"implicit provider": {"--zarlcode-provider", ""}, "implicit model": {"--zarlcode-model", ""},
		"no artifacts": {"--harness-evidence-dir", ""}, "implicit state": {"--state-db", ""},
		"env": {"--env", "secrets.env"}, "mixed drivers": {"--drivers", "zarlcode,zarlcode"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, append(append([]string{}, args...), flags...)...)
			if !errors.Is(err, harness.ErrExperiment) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRefinementRejectsDefaultStateAndAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := statedb.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("do not open this fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{path, alias} {
		_, err := parse(t, append(refinementArgs(t), "--state-db", state)...)
		if !errors.Is(err, harness.ErrExperiment) {
			t.Fatalf("default/alias accepted: %v", err)
		}
	}
}

func TestRefinementUsesCanonicalHomeResolution(t *testing.T) {
	t.Setenv("HOME", "relative-home")
	path, err := statedb.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	_, err = parse(t, append(refinementArgs(t), "--state-db", path)...)
	if !errors.Is(err, harness.ErrExperiment) {
		t.Fatalf("relative HOME bypassed default-state restriction: %v", err)
	}
}
