package manifest_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/manifest"
	"github.com/zarldev/zarlmono/swebench-eval/task"
)

func TestManifestPreservesSelectedInputsAndExcludesPrivateConfiguration(t *testing.T) {
	const secret = "PRIVATE_CONFIGURATION_CANARY"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	cfg, err := evalconfig.Parse(fs, []string{
		"--tasks", secret, "--env", secret, "--state-db", secret,
		"--llamacpp-reset-url", "https://user:" + secret + "@example.invalid/reset",
		"--score-python", secret, "--db", secret, "--run-notes", secret,
		"--worktree-dir", secret, "--clone-cache", secret,
		"--zarlcode-verify-workdir", secret, "--zarlcode-transcript-dir", secret,
		"--ablations", "judge,baseline", "--sample", "2", "--max-iter", "17",
		"--task-timeout", "7m", "--zarlcode-model", "fixture-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	specs := []task.Spec{
		{InstanceID: "second", Repo: "owner/repo", BaseCommit: "commit-two", ProblemStatement: "repair B", TestPatch: "test B", Language: "go"},
		{InstanceID: "first", Repo: "owner/repo", BaseCommit: "commit-one", ProblemStatement: "repair A", Language: "rust"},
	}
	drivers := []string{"zarlcode-judge", "zarlcode"}
	data, err := manifest.Marshal(cfg, specs, drivers)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("manifest copied private configuration")
	}
	var got struct {
		FormatVersion int            `json:"format_version"`
		Tasks         []task.Spec    `json:"tasks"`
		TasksSHA256   string         `json:"tasks_sha256"`
		Drivers       []string       `json:"drivers"`
		Requested     map[string]any `json:"requested"`
		Build         struct {
			GoVersion string `json:"go_version"`
		} `json:"build"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != 1 || len(got.Tasks) != 2 || got.Tasks[0] != specs[0] || got.Tasks[1] != specs[1] {
		t.Fatalf("selected tasks changed: %+v", got.Tasks)
	}
	if strings.Join(got.Drivers, ",") != "zarlcode-judge,zarlcode" {
		t.Fatalf("expanded driver order: %v", got.Drivers)
	}
	tasksJSON, err := json.Marshal(got.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	if got.TasksSHA256 != fmt.Sprintf("%x", sha256.Sum256(tasksJSON)) {
		t.Fatal("task fingerprint cannot be reproduced from exported inputs")
	}
	if got.Requested["max_iterations"] != float64(17) || got.Requested["task_timeout"] != "7m0s" || got.Requested["model"] != "fixture-model" || got.Requested["provider"] != "" {
		t.Fatalf("requested settings: %+v", got.Requested)
	}
	if got.Requested["env_file"] != true || got.Requested["reset_requested"] != true || got.Build.GoVersion == "" {
		t.Fatal("configuration presence or available build identity missing")
	}
}

func TestManifestDeterminismAndTaskIdentity(t *testing.T) {
	cfg := evalconfig.Config{}
	specs := []task.Spec{{InstanceID: "second", BaseCommit: "two"}, {InstanceID: "first", BaseCommit: "one"}}
	drivers := []string{"zarlcode"}
	data, err := manifest.Marshal(cfg, specs, drivers)
	if err != nil {
		t.Fatal(err)
	}
	again, err := manifest.Marshal(cfg, specs, drivers)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("identical capture is not deterministic: %v", err)
	}
	specs[0], specs[1] = specs[1], specs[0]
	reordered, err := manifest.Marshal(cfg, specs, drivers)
	if err != nil {
		t.Fatal(err)
	}
	if taskFingerprint(t, reordered) == taskFingerprint(t, data) {
		t.Fatal("task reordering did not change fingerprint")
	}
	specs[0].BaseCommit = "different-commit"
	changed, err := manifest.Marshal(cfg, specs, drivers)
	if err != nil {
		t.Fatal(err)
	}
	if taskFingerprint(t, reordered) == taskFingerprint(t, changed) {
		t.Fatalf("changed task identity was lost: %v", err)
	}
}

func taskFingerprint(t *testing.T, data []byte) string {
	t.Helper()
	var got struct {
		TasksSHA256 string `json:"tasks_sha256"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got.TasksSHA256
}
