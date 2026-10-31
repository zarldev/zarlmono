package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCLIExperimentRecordsTrialsWithoutProviderCalls(t *testing.T) {
	dir := t.TempDir()
	tasks := filepath.Join(dir, "tasks.jsonl")
	input := filepath.Join(dir, "experiment.json")
	database := filepath.Join(dir, "eval.db")
	evidence := filepath.Join(dir, "evidence")
	// Invalid local task ID fails before cloning/provider construction for every
	// trial. An unknown provider would fail if the execution path reached it.
	for path, body := range map[string]string{
		tasks: `{"instance_id":".","repo":"owner/repo","base_commit":"base","patch":"GOLD-CANARY","FAIL_TO_PASS":"GRADER-CANARY"}`,
		input: `{"version":1,"repeats":2,"candidates":[{"label":"navigation","guidance":"Read first."}]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--tasks", tasks,
		"--harness-experiment", input, "--harness-evidence-dir", evidence,
		"--zarlcode-provider", "nonexistent-offline-fixture", "--zarlcode-model", "fixed",
		"--db", database, "--run-id", "experiment", "--worktree-dir", filepath.Join(dir, "work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("experiment: %v\n%s", err, output)
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(t.Context(), "go", "run", ".", "--export-run", "experiment", "--db", database, "--harness-experiment", input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Manifest struct {
			Version           int      `json:"format_version"`
			Drivers           []string `json:"drivers"`
			EvidenceNamespace string   `json:"evidence_namespace"`
			Trials            []struct {
				Driver   string `json:"driver"`
				Guidance string `json:"guidance"`
				Digest   string `json:"guidance_sha256"`
			} `json:"harness_trials"`
		} `json:"manifest"`
		Results []struct {
			Error    string `json:"error"`
			Resolved *bool  `json:"resolved"`
		} `json:"results"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"zarlcode-harness-baseline-r1", "zarlcode-harness-navigation-r1", "zarlcode-harness-baseline-r2", "zarlcode-harness-navigation-r2"}
	if got.Manifest.Version != 2 || !slices.Equal(got.Manifest.Drivers, want) || len(got.Manifest.Trials) != 4 || len(got.Results) != 4 {
		t.Fatalf("trial identity lost: %s", output)
	}
	if got.Manifest.Trials[1].Guidance != "Read first." || len(got.Manifest.Trials[1].Digest) != 64 {
		t.Fatal("candidate snapshot lost")
	}
	for _, result := range got.Results {
		if result.Error == "" || result.Resolved != nil {
			t.Fatal("materialization failure lost or scored")
		}
	}
	snapshot, err := os.ReadFile(filepath.Join(evidence, got.Manifest.EvidenceNamespace, "experiment.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snapshot), "CANARY") || !strings.Contains(string(snapshot), "Read first.") {
		t.Fatalf("snapshot contains grading metadata or lost guidance: %s", snapshot)
	}
}
