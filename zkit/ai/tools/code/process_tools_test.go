package code_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// procRoot is a tiny code.ProcessWorkspace impl — black-box tests
// can't reach the in-package fakeProcessWorkspace, so we declare a
// local one with the same single Root() method.
type procRoot struct{ root string }

func (p procRoot) Root() string { return p.root }

// newProcMgrForTest mirrors newTestProcessManager but lives in the
// _test package and uses the public option setters.
func newProcMgrForTest(t *testing.T) *code.ProcessManager {
	t.Helper()
	mgr := code.NewProcessManager(procRoot{root: t.TempDir()},
		code.WithReapAfter(500*time.Millisecond),
		code.WithMaxAliveProcesses(4),
		code.WithProcessOutputBuffer(64),
	)
	t.Cleanup(func() { mgr.KillAll(t.Context()) })
	return mgr
}

// waitForExit polls m.Info until the process exits or the deadline fires.
func waitForExit(t *testing.T, m *code.ProcessManager, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := m.Info(id)
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if !info.Running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %s did not exit within %s", id, timeout)
}

func bashOutputText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	r, ok := res.Data.(code.BashOutputResult)
	if !ok {
		t.Fatalf("Data is %T, want code.BashOutputResult", res.Data)
	}
	return r.String()
}

func listProcsText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	r, ok := res.Data.(code.ListProcessesResult)
	if !ok {
		t.Fatalf("Data is %T, want code.ListProcessesResult", res.Data)
	}
	return r.String()
}

// Default bash_output is labelled: a `process:` header then `--- stdout ---`
// sections with the captured lines.
func TestBashOutput_DefaultOutputShape(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	id, err := mgr.StartProcess(`echo hello; echo world`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	waitForExit(t, mgr, id, 3*time.Second)

	tool := code.NewBashOutputTool(mgr)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID: "c", ToolName: "bash_output",
		Arguments: tools.ToolParameters{"process_id": id},
	})
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := bashOutputText(t, res)
	if strings.HasPrefix(body, "[") || strings.HasPrefix(body, "{") {
		t.Errorf("output looks JSON-shaped (starts with bracket): %q", body)
	}
	if !strings.HasPrefix(body, "process: ") {
		t.Errorf("output should start with `process: <id>` header: %q", body)
	}
	if !strings.Contains(body, "--- stdout") {
		t.Errorf("stdout section header missing: %q", body)
	}
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Errorf("captured stdout lines missing: %q", body)
	}
}

// output="json" renders the snapshot object instead of labelled sections.
func TestBashOutput_JSONOutput(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	id, err := mgr.StartProcess(`echo hello`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	waitForExit(t, mgr, id, 3*time.Second)

	tool := code.NewBashOutputTool(mgr)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID: "c", ToolName: "bash_output",
		Arguments: tools.ToolParameters{"process_id": id, "output": "json"},
	})
	body := bashOutputText(t, res)
	if !strings.HasPrefix(body, "{") {
		t.Errorf("json output should be an object: %q", body)
	}
	var snap struct {
		ProcessID string   `json:"process_id"`
		Stdout    []string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("json output should parse: %v\n%s", err, body)
	}
	if snap.ProcessID != id {
		t.Errorf("process_id = %q, want %q", snap.ProcessID, id)
	}
}

func TestBashOutput_MissingProcessRejected(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	tool := code.NewBashOutputTool(mgr)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID: "c", ToolName: "bash_output",
		Arguments: tools.ToolParameters{"process_id": "p-does-not-exist"},
	})
	if res.Success {
		t.Fatalf("missing process: want failure, got %+v", res)
	}
}

func TestListProcesses_DefaultOutputShape(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	_, err := mgr.StartProcess(`sleep 5`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}

	tool := code.NewListProcessesTool(mgr)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID: "c", ToolName: "list_processes",
	})
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := listProcsText(t, res)
	if !strings.HasPrefix(body, "processes: ") {
		t.Errorf("output should start with `processes: N` header: %q", body)
	}
	if !strings.Contains(body, "pid ") {
		t.Errorf("process row should carry `pid <n>`: %q", body)
	}
	if !strings.Contains(body, "cwd: ") {
		t.Errorf("cwd row missing: %q", body)
	}
	if !strings.Contains(body, "stdout: ") || !strings.Contains(body, "stderr: ") {
		t.Errorf("line-count row missing: %q", body)
	}
}

// output="json" renders the {processes, count} object.
func TestListProcesses_JSONOutput(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	_, err := mgr.StartProcess(`sleep 5`)
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	tool := code.NewListProcessesTool(mgr)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID: "c", ToolName: "list_processes",
		Arguments: tools.ToolParameters{"output": "json"},
	})
	body := listProcsText(t, res)
	if !strings.HasPrefix(body, "{") {
		t.Errorf("json output should be an object: %q", body)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("json output should parse: %v\n%s", err, body)
	}
	if payload.Count != 1 {
		t.Errorf("count = %d, want 1", payload.Count)
	}
}

func TestListProcesses_EmptySentinel(t *testing.T) {
	t.Parallel()
	mgr := newProcMgrForTest(t)
	tool := code.NewListProcessesTool(mgr)
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID: "c", ToolName: "list_processes",
	})
	body := listProcsText(t, res)
	if !strings.Contains(body, "processes: 0") {
		t.Errorf("count header missing: %q", body)
	}
	if !strings.Contains(body, "(none running)") {
		t.Errorf("empty sentinel missing: %q", body)
	}
}
