package code_test

import (
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestBash_Background_StartsDetached(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	bash := code.NewBashTool(ws, code.WithProcessManager(mgr))

	// Run a sleep that would otherwise block the call far past test
	// timeout. Background should return immediately with a pid + log.
	res, err := bash.Execute(t.Context(), tools.ToolCall{
		ID:        "tc1",
		Arguments: tools.ToolParameters{"command": "sleep 60", "background": true},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("Execute returned failure: %q", res.Data)
	}

	body, ok := res.Data.(string)
	if !ok {
		t.Fatalf("Data type %T, want string", res.Data)
	}
	if !strings.Contains(body, "started process_id=") {
		t.Errorf("body = %q, want managed process line", body)
	}

	pid := extractPID(t, body)
	process := assertOneProcessEffect(t, res)
	if !process.Background || process.PID != pid || process.Command != "sleep 60" {
		t.Fatalf("process effect = %+v, want background pid=%d command=sleep 60", process, pid)
	}
	if _, err := mgr.Info(code.ProcessID(process.ProcessID)); err != nil {
		t.Errorf("managed process %q not registered: %v", process.ProcessID, err)
	}

	// Verify the process is actually alive.
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("background pid %d not alive: %v", pid, err)
	}
}

func extractPID(t *testing.T, body string) int {
	t.Helper()
	const prefix = "pid="
	_, after, ok := strings.Cut(body, prefix)
	if !ok {
		t.Fatalf("no pid in body: %q", body)
	}
	rest := after
	end := strings.IndexAny(rest, " \n\t")
	if end < 0 {
		end = len(rest)
	}
	pid, err := strconv.Atoi(rest[:end])
	if err != nil {
		t.Fatalf("parse pid %q: %v", rest[:end], err)
	}
	return pid
}

func TestBash_RedactsSecretsFromForegroundOutput(t *testing.T) {
	t.Parallel()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	bash := code.NewBashTool(ws)

	res, err := bash.Execute(t.Context(), tools.ToolCall{
		ID:        "tc1",
		Arguments: tools.ToolParameters{"command": `printf 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456\n'`},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	body, ok := res.Data.(string)
	if !ok {
		t.Fatalf("Data type %T, want string", res.Data)
	}
	if strings.Contains(body, "abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("bash output leaked bearer token: %q", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("bash output missing redaction marker: %q", body)
	}
	process := assertOneProcessEffect(t, res)
	if process.Background || process.ExitCode != 0 || process.Command == "" {
		t.Fatalf("process effect = %+v, want foreground exit 0 with command", process)
	}
}

func TestBash_ForegroundFailureEmitsProcessEffect(t *testing.T) {
	t.Parallel()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	bash := code.NewBashTool(ws)

	res, err := bash.Execute(t.Context(), tools.ToolCall{
		ID:        "tc1",
		Arguments: tools.ToolParameters{"command": "exit 7"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	process := assertOneProcessEffect(t, res)
	if process.ExitCode != 7 || process.Background {
		t.Fatalf("process effect = %+v, want foreground exit 7", process)
	}
}

func assertOneProcessEffect(t *testing.T, res *tools.ToolResult) tools.ProcessEffect {
	t.Helper()
	if !res.Success {
		t.Fatalf("result failed: %s", res.Error)
	}
	processes := res.ProcessEffects()
	if len(processes) != 1 {
		t.Fatalf("ProcessEffects len = %d, want 1 (%+v)", len(processes), processes)
	}
	return processes[0]
}

func TestBashOutputResult_RedactsSecrets(t *testing.T) {
	r := code.BashOutputResult{
		Snapshot: code.OutputSnapshot{
			ID:      "p1",
			Running: false,
			Stdout:  []string{"GITHUB_TOKEN=ghp_0123456789abcdefghijklmnop", "ok"},
			Stderr:  []string{"warning: api_key=supersecretvalue"},
		},
	}
	// Labeled (default) form.
	got := r.String()
	if strings.Contains(got, "ghp_0123456789abcdefghijklmnop") || strings.Contains(got, "supersecretvalue") {
		t.Errorf("labeled bash_output leaked a secret:\n%s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("labeled bash_output should contain [REDACTED]:\n%s", got)
	}
	// JSON form.
	rj := r
	rj.Output = tools.OutputJSON
	if j := rj.String(); strings.Contains(j, "ghp_0123456789abcdefghijklmnop") || strings.Contains(j, "supersecretvalue") {
		t.Errorf("json bash_output leaked a secret:\n%s", j)
	}
}
