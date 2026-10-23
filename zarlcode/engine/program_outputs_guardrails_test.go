package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProgramOutputRecoveryReaderHonorsGuardrails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, ".zarlcode", "hooks", "recovery.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`---
name: recovery-policy
description: block recovery pages in this test
event: pre_tool
matcher: program_output_read
blocking: true
---
printf 'recovery-policy-blocked'
exit 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := newMCPSettings(t)
	setMCPSetting(t, settings, prefs.KeyProgramOutputRecovery, "on")
	requests := 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		requests++
		switch requests {
		case 1:
			return modeCalls(namedModeCall("emit", "program", `{"script":"emit(\"x\" * 70000)"}`))
		case 2:
			receipt := recoveryReceipt(t, modeResult(req.Messages, "emit"))
			return modeCalls(namedModeCall("blocked", "program_output_read", fmt.Sprintf(`{"receipt":%q,"limit":4}`, receipt)))
		default:
			if got := modeResult(req.Messages, "blocked"); !strings.Contains(got, "recovery-policy-blocked") || strings.Contains(got, "total_bytes") {
				t.Errorf("reader bypassed policy: %s", got)
			}
			return modeCalls()
		}
	})
	live := modeLive(t, root, provider, engine.WithSettings(settings))
	if err := live.RunTurn(t.Context(), "test reader policy"); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d", requests)
	}
}
