package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/task"
)

func writeFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.jsonl")
	if err := writeFile(path, strings.Join(lines, "\n")+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoad_HappyPath(t *testing.T) {
	path := writeFixture(t,
		`{"instance_id":"a","repo":"r/a","base_commit":"a1","problem_statement":"p","language":"go"}`,
		`{"instance_id":"b","repo":"r/b","base_commit":"b1","problem_statement":"q","language":"java"}`,
	)
	specs, err := task.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("len = %d, want 2", len(specs))
	}
	if specs[0].InstanceID != "a" || specs[1].Language != "java" {
		t.Errorf("unexpected specs: %+v", specs)
	}
}

func TestLoad_SkipsBlankLines(t *testing.T) {
	path := writeFixture(t,
		`{"instance_id":"a"}`,
		``,
		`{"instance_id":"b"}`,
	)
	specs, err := task.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(specs) != 2 {
		t.Errorf("len = %d, want 2 (blank line should be skipped)", len(specs))
	}
}

func TestLoad_BadJSONErrors(t *testing.T) {
	path := writeFixture(t,
		`{"instance_id":"a"}`,
		`{not valid json`,
	)
	if _, err := task.Load(path); err == nil {
		t.Error("Load: want error on malformed JSON, got nil")
	}
}

func TestFilterByLanguage(t *testing.T) {
	specs := []task.Spec{
		{InstanceID: "a", Language: "go"},
		{InstanceID: "b", Language: "Java"},
		{InstanceID: "c", Language: "rust"},
		{InstanceID: "d", Language: "GO"},
	}
	got := task.FilterByLanguage(specs, "go", "java")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestFilterByLanguage_EmptyPassThrough(t *testing.T) {
	specs := []task.Spec{{InstanceID: "a"}, {InstanceID: "b"}}
	got := task.FilterByLanguage(specs)
	if len(got) != len(specs) {
		t.Errorf("empty filter should pass through; got %d, want %d", len(got), len(specs))
	}
}

func TestSample(t *testing.T) {
	specs := make([]task.Spec, 100)
	for i := range specs {
		specs[i].InstanceID = string(rune('A' + i%26))
	}
	got := task.Sample(specs, 10)
	if len(got) != 10 {
		t.Errorf("len = %d, want 10", len(got))
	}
}

func TestSample_NLargerThanInputReturnsAll(t *testing.T) {
	specs := []task.Spec{{}, {}, {}}
	got := task.Sample(specs, 100)
	if len(got) != len(specs) {
		t.Errorf("oversize sample should return input; got %d, want %d", len(got), len(specs))
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
