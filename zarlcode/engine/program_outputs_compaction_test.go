package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProgramOutputRecoverySurvivesTieredCompaction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	setMCPSetting(t, settings, prefs.KeyProgramOutputRecovery, "on")
	setMCPSetting(t, settings, prefs.KeyCompactEngine, "tiered")
	setMCPSetting(t, settings, prefs.KeyCompactProvider, "(active)")
	var call llm.ToolCall
	var result string
	requests, emissions := 0, 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		requests++
		if requests == 1 && call.ID != "" {
			if call.Function.Name == "program" {
				emissions++
			}
			return modeCalls(call)
		}
		result = modeResult(req.Messages, call.ID)
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "ASSISTANT_HEAD" + strings.Repeat("a", 6000) + "ASSISTANT_MIDDLE" + strings.Repeat("b", 6000) + "ASSISTANT_TAIL", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	live := modeLive(t, t.TempDir(), provider, engine.WithSettings(settings))
	live.SetLimits(0, 4, 4, 0)
	// Accumulate uncompressed fixture history, then force real tiered pressure
	// through the public host operation without replacing the conversation.
	live.SetContextWindow(1000000)
	run := func(prompt string) {
		t.Helper()
		requests, result = 0, ""
		if err := live.RunTurn(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	call = namedModeCall("retained", "program", `{"script":"emit(\"HEAD\" + \"x\" * 35000 + \"MIDDLE_FACT\" + \"y\" * 35000 + \"TAIL_FACT\")"}`)
	run("retain the fixture")
	originalReceipt := recoveryReceipt(t, result)
	call = llm.ToolCall{}
	for range 8 {
		run("continue the offline fixture")
	}
	before := live.ContextSnapshot()
	marshal := func(messages []llm.Message) []byte {
		t.Helper()
		data, err := json.Marshal(messages)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	beforeBytes := len(marshal(before))
	live.SetContextWindow(1000)
	summary, err := live.CompactNow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	after := live.ContextSnapshot()
	afterBytes := len(marshal(after))
	if afterBytes >= beforeBytes || summary.BytesTrimmed <= 0 || summary.Engine != "tiered" {
		t.Fatalf("tiered compaction did not reduce history: %d -> %d: %+v", beforeBytes, afterBytes, summary)
	}
	if _, repaired := compact.RepairToolPairing(after); repaired != 0 {
		t.Fatal("compaction broke tool pairs")
	}
	// Derive the receipt from compacted history, not a hidden saved identifier.
	receipt := recoveryReceipt(t, modeResult(after, "retained"))
	if receipt != originalReceipt {
		t.Fatal("compaction changed receipt")
	}
	live.SetContextWindow(1000000)
	payload := "HEAD" + strings.Repeat("x", 35000) + "MIDDLE_FACT" + strings.Repeat("y", 35000) + "TAIL_FACT"
	for _, fact := range []string{"MIDDLE_FACT", "TAIL_FACT"} {
		offset := strings.Index(payload, fact) + 1 // Serialized JSON starts with a quote.
		call = namedModeCall("read-"+fact, "program_output_read", fmt.Sprintf(`{"receipt":%q,"offset":%d,"limit":%d}`, receipt, offset, len(fact)))
		run("recover the retained fact")
		var page program.OutputPage
		if err := json.Unmarshal([]byte(result), &page); err != nil {
			t.Fatalf("page %s: %v", result, err)
		}
		if page.Content != fact || page.NextOffset != offset+len(fact) || page.TotalBytes != len(payload)+2 {
			t.Fatalf("page: %+v", page)
		}
	}
	if emissions != 1 {
		t.Fatalf("program reran %d times", emissions)
	}
	t.Logf("message-array JSON: %d -> %d bytes (%.1f%% reduction); original emission count=%d; middle/tail facts recovered", beforeBytes, afterBytes, 100*float64(beforeBytes-afterBytes)/float64(beforeBytes), emissions)
}
