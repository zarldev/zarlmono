package coderisk_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/coderisk"
)

func TestCommandRequiresCompleteCollectionAndDoesNotGateScores(t *testing.T) {
	t.Parallel()
	root, inventory := fixture(t, map[string]string{
		"sample.go": "package sample\nfunc Complex() {\n" + strings.Repeat("if true { println(1) }\n", 19) + "}\n",
	})
	input := t.TempDir()
	binary := filepath.Join(t.TempDir(), "coderisk")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/coderisk")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	run := func() error {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, "-root", root, "-input", input, "-top", "1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("CLI: %s", output)
		}
		return err
	}
	assertNoReport := func() {
		t.Helper()
		for _, name := range []string{"report.md", "report.json"} {
			if _, err := os.Stat(filepath.Join(input, name)); !os.IsNotExist(err) {
				t.Fatalf("incomplete collection produced %s (stat error: %v)", name, err)
			}
		}
	}
	if err := run(); err == nil {
		t.Fatal("CLI accepted collection without completion marker")
	}
	assertNoReport()
	writeFile(t, input, "complete", "")
	writeFile(t, input, "environment.json", `{"GOOS":"fixture","GOARCH":"fixture","GOVERSION":"test"}`)
	modules := []string{"root", "examples", "zkit", "zarlcode", "swebench-eval"}
	for _, module := range modules {
		writeFile(t, input, module+".packages.json", string(inventory))
		if module != "swebench-eval" {
			writeFile(t, input, module+".cover", "mode: atomic\nexample.test/sample/sample.go:3.1,3.22 1 0\n")
		}
	}
	if err := run(); err == nil {
		t.Fatal("CLI accepted missing module coverage")
	}
	assertNoReport()
	writeFile(t, input, "swebench-eval.cover", "mode: atomic\nexample.test/sample/sample.go:3.1,3.22 1 0\n")
	if err := run(); err != nil {
		t.Fatalf("report-only CLI rejected valid high-risk functions: %v", err)
	}
	encoded, err := os.ReadFile(filepath.Join(input, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report coderisk.Report
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Functions) != len(modules) {
		t.Fatalf("got %d functions, want one per module", len(report.Functions))
	}
	checkMetric(t, "high CRAP", report.Functions[0].CRAP, 420)
	markdown, err := os.ReadFile(filepath.Join(input, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "420.00") {
		t.Fatalf("high risk missing from Markdown: %s", markdown)
	}
}
