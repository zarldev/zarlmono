package tui_test

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestFailedSwarmActivationCommitsDurableSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		var calls atomic.Int32
		client := &http.Client{Transport: settlementTransport(func(request *http.Request) (*http.Response, error) {
			body := "data: " + `{"type":"response.completed","response":{"output":[{"type":"message","id":"seed","role":"assistant","content":[{"type":"output_text","text":"seed answer"}]}],"usage":{}}}` + "\n\n"
			if calls.Add(1) > 1 {
				body = "data: " + `{"type":"response.output_text.delta","output_index":0,"delta":"partial swarm answer"}` + "\n\n" +
					"data: " + `{"type":"response.failed","response":{"error":{"code":"server_error","message":"fixture failure"}}}` + "\n\n"
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}
		provider := openai.NewProvider("fixture-key", openai.WithHTTPClient(client), openai.WithModel("gpt-5.6"))
		f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "gpt-5.6"})
		settleRewindTurn(t, f, "seed exact session")
		seed, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		before, err := rewind.DecodeResume(seed.ContextJSON)
		if err != nil {
			t.Fatal(err)
		}
		f.ui.StartSwarm("research with a failing provider")
		defer func() {
			f.ui.StopSwarm()
			synctest.Wait()
		}()
		for range 10 {
			synctest.Wait()
			f.sink.Drain()
			f.mu.Lock()
			events := f.events
			f.events = nil
			f.mu.Unlock()
			if len(events) == 0 {
				break
			}
			for i, event := range events {
				_, cmd := f.ui.Update(event)
				if i == len(events)-1 {
					driveBeforeCommand(f, cmd)
				}
			}
		}
		if calls.Load() < 2 {
			t.Fatal("swarm activation did not dispatch")
		}
		saved, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		after, err := rewind.DecodeResume(saved.ContextJSON)
		if err != nil {
			t.Fatal(err)
		}
		if after.SettledTurnID == before.SettledTurnID || after.EventWatermark <= before.EventWatermark {
			t.Fatalf("failed activation did not advance durable settlement: before=%s/%d after=%s/%d", before.SettledTurnID, before.EventWatermark, after.SettledTurnID, after.EventWatermark)
		}
		thread, err := f.store.GetSessionTranscript(t.Context(), saved.ID)
		if err != nil || thread.Revision != after.Revision {
			t.Fatalf("durable transcript/head mismatch: %v", err)
		}
	})
}
