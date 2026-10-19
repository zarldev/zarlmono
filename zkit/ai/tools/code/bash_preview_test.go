package code_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func bashPreviewGate(t *testing.T) (string, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return path, file
}

func executeWithPreview(t *testing.T, command string, callback func(tools.ToolOutputUpdate)) (*tools.ToolResult, []string) {
	t.Helper()
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := code.NewBashTool(workspace)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var snapshots []string
	result, err := tool.Execute(ctx, tools.ToolCall{ID: "bash-preview", ExecutionID: "exact-execution", Arguments: tools.ToolParameters{"command": command, "timeout_seconds": 3}, ReportOutput: func(_ context.Context, update tools.ToolOutputUpdate) {
		mu.Lock()
		snapshots = append(snapshots, update.Text)
		mu.Unlock()
		if callback != nil {
			callback(update)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	owned := append([]string(nil), snapshots...)
	mu.Unlock()
	if len(owned) > 256 {
		t.Fatalf("unbounded preview update count: %d", len(owned))
	}
	for _, snapshot := range owned {
		if len(snapshot) > tools.MaxToolOutputPreviewBytes || !utf8.ValidString(snapshot) {
			t.Fatal("preview size/UTF-8 contract violated")
		}
	}
	return result, owned
}

func TestBashPreviewArrivesBeforeExitAndKeepsStreamsSeparate(t *testing.T) {
	gatePath, gate := bashPreviewGate(t)
	var release sync.Once
	command := fmt.Sprintf(`printf 'token='; printf 'ordinary-stderr\n' >&2; read -r signal < %q; printf 'SPLIT_SECRET_CANARY\n'; printf 'safe-stdout\n'`, gatePath)
	result, snapshots := executeWithPreview(t, command, func(update tools.ToolOutputUpdate) {
		if strings.Contains(update.Text, "ordinary-stderr") {
			release.Do(func() {
				if _, err := gate.WriteString("continue\n"); err != nil {
					t.Error(err)
				}
			})
		}
	})
	if len(snapshots) == 0 {
		t.Fatal("no output before child awaited its release")
	}
	joined := strings.Join(snapshots, "\n")
	if strings.Contains(joined, "SPLIT_SECRET_CANARY") || !strings.Contains(joined, "[stderr] ordinary-stderr") || !strings.Contains(joined, "safe-stdout") {
		t.Fatalf("stream interleave confused redaction or omitted ordinary output: %s", joined)
	}
	if strings.Contains(result.Data.(string), "SPLIT_SECRET_CANARY") {
		t.Fatal("final merged output exposed a credential hidden by the preview")
	}
	if result == nil || !strings.Contains(result.Data.(string), "[exit 0]") {
		t.Fatalf("gated command did not finish: %+v", result)
	}
}

func TestBashPreviewRedactsSplitPrivateAndCredentialLines(t *testing.T) {
	for _, command := range []string{
		`printf 'Auth'; printf 'orization: Bear'; printf 'er short\n'; printf 'token=abc\napi_key="QUOTED_SECRET_CANARY"\n'; printf 'safe\n'`,
		`printf 'Bearer\nSPLIT_SECRET_CANARY\n'; printf 'token=\n\nNEXT_LINE_CANARY\n'; printf 'safe\n'`,
		`printf '%s\n' '-----BEGIN PRIVATE KEY-----' 'PRIVATE_BODY_CANARY' '-----END PRIVATE KEY-----' 'safe'`,
		`printf '%s\n' '-----BEGIN RSA PRIVATE KEY-----' 'PRIVATE_BODY_CANARY'`,
	} {
		result, snapshots := executeWithPreview(t, command, nil)
		for _, snapshot := range snapshots {
			for _, secret := range []string{"short", "token=abc", "QUOTED_SECRET_CANARY", "SPLIT_SECRET_CANARY", "NEXT_LINE_CANARY", "PRIVATE_BODY_CANARY"} {
				if strings.Contains(snapshot, secret) {
					t.Fatalf("intermediate preview exposed %q", secret)
				}
			}
		}
		if len(snapshots) == 0 {
			t.Fatal("sensitive output produced no honest redaction marker")
		}
		if result == nil {
			t.Fatal("missing final command result")
		}
	}
}

func TestBashPreviewOmitsUnsafeOrUnterminatedOutputButStillDrains(t *testing.T) {
	for _, test := range []struct{ command, notice string }{
		{`printf '\033[2Junsafe\n'; printf 'after\n'`, "terminal controls omitted"},
		{`printf 'progress\rrewritten\n'`, "terminal controls omitted"},
		{`printf '\377\n'`, "invalid UTF-8"},
		{`printf '%09000d\n' 0; printf 'after\n'`, "oversized output line"},
		{`printf 'UNTERMINATED_CANARY'`, "unterminated or sensitive output"},
	} {
		result, snapshots := executeWithPreview(t, test.command, nil)
		joined := strings.Join(snapshots, "\n")
		if !strings.Contains(joined, test.notice) || strings.Contains(joined, "\x1b") || strings.Contains(joined, "UNTERMINATED_CANARY") {
			t.Fatalf("unsafe preview missing truthful omission: %q", joined)
		}
		if !strings.Contains(result.Data.(string), "[exit 0]") {
			t.Fatalf("preview policy stopped process draining: %+v", result)
		}
	}
}

func TestBashPreviewBudgetDoesNotStopFinalOutputCapture(t *testing.T) {
	result, snapshots := executeWithPreview(t, `i=0; while [ "$i" -lt 8000 ]; do printf 'safe line\n'; i=$((i+1)); done`, nil)
	if len(snapshots) == 0 || !strings.Contains(snapshots[len(snapshots)-1], "preview stopped") {
		t.Fatal("bounded preview did not advertise its limit")
	}
	final := result.Data.(string)
	if strings.Count(final, "safe line") != 8000 || !strings.Contains(final, "[exit 0]") {
		t.Fatal("live preview limit changed authoritative final capture")
	}
}

func TestBashPreviewCancellationJoinsWritersBeforeReturn(t *testing.T) {
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("synthetic stop")
	var mu sync.Mutex
	updates := 0
	result, err := code.NewBashTool(workspace).Execute(ctx, tools.ToolCall{ID: "cancel", Arguments: tools.ToolParameters{"command": `printf 'cancel-ready\n'; sleep 60`}, ReportOutput: func(_ context.Context, update tools.ToolOutputUpdate) {
		mu.Lock()
		updates++
		mu.Unlock()
		if strings.Contains(update.Text, "cancel-ready") {
			cancel(cause)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || ctx.Err() == nil {
		t.Fatal("callback did not stop owned command")
	}
	mu.Lock()
	count := updates
	mu.Unlock()
	if count == 0 {
		t.Fatal("missing pre-cancel output")
	}
}
