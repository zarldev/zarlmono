package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/refine"
	"github.com/zarldev/zarlmono/swebench-eval/task"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	statedb "github.com/zarldev/zarlmono/zkit/db"
)

func TestCLIRefinementLoopbackIsolatesProbesAndGrading(t *testing.T) {
	t.Setenv("ZARLCODE_SANDBOX", "0") // Fixture emits only a workspace-scoped write tool.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file") // A broken rewrite must never fetch GitHub.
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed")
	if err := os.Mkdir(seed, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", seed}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "base")
	base := git("rev-parse", "HEAD")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+seed+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/refinement-fixture/repo")
	server, calls := refinementServer(t)
	t.Cleanup(server.Close)
	state := filepath.Join(dir, "isolated.db")
	store, err := statedb.Open(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertProvider(t.Context(), backends.StoredProvider{Name: "loopback-refinement", AdapterType: "openAICompatible", BaseURL: server.URL, DefaultModel: "fixture-model", Enabled: true}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	tasks := filepath.Join(dir, "tasks.jsonl")
	data, err := json.Marshal(task.Spec{InstanceID: "fixture-task", Repo: "refinement-fixture/repo", BaseCommit: base, ProblemStatement: "Create answer.txt.", PatchGold: "GOLD_ANSWER_CANARY", TestPatch: "GOLD_ANSWER_CANARY", FailToPass: "GRADING_LIST_CANARY"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(tasks, data, 0600); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(dir, "evidence")
	database := filepath.Join(dir, "eval.db")
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "--harness-refine", "--tasks", tasks, "--sample", "1", "--max-iter", "3", "--task-timeout", "10s", "--harness-improver-timeout", "5s", "--zarlcode-provider", "loopback-refinement", "--zarlcode-model", "fixture-model", "--state-db", state, "--harness-evidence-dir", evidence, "--db", database, "--run-id", "refinement", "--worktree-dir", filepath.Join(dir, "work"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refinement CLI: %v\n%s", err, output)
	}
	if calls.solver.Load() != 8 || calls.improver.Load() != 2 {
		t.Fatalf("calls solver=%d improver=%d", calls.solver.Load(), calls.improver.Load())
	}
	assertRefinementResults(t, database)
	assertRefinementJournal(t, evidence)
}

type refinementCalls struct{ solver, improver atomic.Int32 }

func refinementServer(t *testing.T) (*httptest.Server, *refinementCalls) {
	t.Helper()
	calls := &refinementCalls{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		if bytes.Contains(data, []byte("GOLD_ANSWER_CANARY")) || bytes.Contains(data, []byte("GRADING_LIST_CANARY")) {
			t.Error("grading material reached provider")
		}
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err = json.Unmarshal(data, &request); err != nil || len(request.Messages) == 0 {
			t.Errorf("request: %v", err)
			http.Error(w, "json", http.StatusBadRequest)
			return
		}
		var delta map[string]any
		if len(request.Tools) == 0 {
			calls.improver.Add(1)
			if bytes.Contains(data, []byte("PRIVATE_TOOL_CANARY")) {
				t.Error("raw tool arguments/diff reached improver")
			}
			var input struct {
				Labels   []string               `json:"labels"`
				Evidence []harness.EvidenceView `json:"evidence"`
			}
			if err = json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &input); err != nil || len(input.Evidence) == 0 {
				t.Errorf("improver projection: %v", err)
				http.Error(w, "projection", http.StatusBadRequest)
				return
			}
			var candidates []map[string]string
			for _, label := range input.Labels {
				candidates = append(candidates, map[string]string{"label": label, "guidance": "Use focused checks for " + label})
			}
			body, _ := json.Marshal(map[string]any{"version": 1, "no_change": false, "rationale": "Contrast supplied metadata.", "evidence_ids": []string{input.Evidence[0].ID}, "experiment": map[string]any{"version": 1, "repeats": 1, "candidates": candidates}})
			delta = map[string]any{"content": string(body)}
		} else {
			calls.solver.Add(1)
			afterTool := false
			for _, message := range request.Messages {
				afterTool = afterTool || message.Role == "tool"
			}
			if afterTool {
				delta = map[string]any{"content": "Created answer.txt."}
			} else {
				delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "write-one", "type": "function", "function": map[string]string{"name": "write", "arguments": `{"path":"answer.txt","content":"PRIVATE_TOOL_CANARY"}`}}}}
			}
		}
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", body)
	}))
	return server, calls
}

func assertRefinementResults(t *testing.T, database string) {
	t.Helper()
	eval, err := db.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	defer eval.Close()
	snapshot, err := eval.ReadRunSnapshot(t.Context(), "refinement")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Results) != 4 {
		t.Fatalf("results=%d", len(snapshot.Results))
	}
	var names []string
	for _, result := range snapshot.Results {
		names = append(names, result.DriverName)
		if result.Error != "" || result.Resolved != nil || !strings.Contains(result.Diff, "PRIVATE_TOOL_CANARY") {
			t.Fatalf("probe not isolated/unscored: %+v", result)
		}
	}
	want := refine.PlannedDrivers()
	slices.Sort(want)
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("driver identities=%v", names)
	}
	var manifest struct {
		Version    int `json:"format_version"`
		Refinement struct {
			Calls    int    `json:"max_improver_calls"`
			Rollouts int    `json:"max_solver_rollouts"`
			Profile  string `json:"evidence_profile"`
		} `json:"refinement"`
	}
	if err = json.Unmarshal([]byte(snapshot.Run.ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 3 || manifest.Refinement.Calls != 2 || manifest.Refinement.Rollouts != 4 || manifest.Refinement.Profile != "metadata-v1" {
		t.Fatalf("refinement manifest: %+v", manifest)
	}
}

func assertRefinementJournal(t *testing.T, evidence string) {
	t.Helper()
	journals, err := filepath.Glob(filepath.Join(evidence, "experiment-*", "round-*.jsonl"))
	if err != nil || len(journals) != 1 {
		t.Fatalf("journals=%v %v", journals, err)
	}
	journal, err := os.ReadFile(journals[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(journal, []byte("PRIVATE_TOOL_CANARY")) || bytes.Contains(journal, []byte("GOLD_ANSWER_CANARY")) {
		t.Fatal("private material in projected stage journal")
	}
	lines := bytes.Split(bytes.TrimSpace(journal), []byte("\n"))
	if len(lines) != 13 || !bytes.Contains(lines[len(lines)-1], []byte(`"state":"frozen"`)) {
		t.Fatalf("incomplete stage journal: %s", journal)
	}
}
