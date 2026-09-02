package report_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/report"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
)

func TestConsoleTaskStatusLabels(t *testing.T) {
	t.Parallel()

	resolved := true
	unresolved := false
	records := []runner.TaskResult{
		{InstanceID: "run-error", DriverName: "driver", Language: "go", Result: harness.Result{Err: errors.New("agent stopped")}},
		{InstanceID: "evaluator-error", DriverName: "driver", Language: "go", Result: harness.Result{Diff: "patch"}, EvaluatorError: "tests crashed"},
		{InstanceID: "resolved", DriverName: "driver", Language: "go", Result: harness.Result{Diff: "patch", Verified: true}, Resolved: &resolved},
		{InstanceID: "unresolved", DriverName: "driver", Language: "go", Result: harness.Result{Diff: "patch", Verified: true}, Resolved: &unresolved},
		{InstanceID: "no-change", DriverName: "driver", Language: "go"},
		{InstanceID: "verified-attempt", DriverName: "driver", Language: "go", Result: harness.Result{Diff: "patch", Verified: true}},
		{InstanceID: "unverified-attempt", DriverName: "driver", Language: "go", Result: harness.Result{Diff: "patch"}},
	}

	var output bytes.Buffer
	report.Console(&output, runner.Results{Records: records})

	for _, want := range []string{
		"run-error", "ERR: agent stopped",
		"evaluator-error", "evaluator-error: tests crashed",
		"resolved", " resolved\n",
		"unresolved", " unresolved\n",
		"no-change", "unscored/no-change",
		"verified-attempt", "unscored/verified-attempt",
		"unverified-attempt", "unscored/unverified-attempt",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("Console output missing %q:\n%s", want, output.String())
		}
	}
}
