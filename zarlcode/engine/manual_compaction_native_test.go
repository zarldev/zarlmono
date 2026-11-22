package engine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestCompactRecordedSessionNativeOpenAIModelOnlyBinding(t *testing.T) {
	var wireModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		wireModels = append(wireModels, request.Model)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"offline summary\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: "summary", prefs.KeyCompactionMode: "manual", prefs.KeyCompactModel: "diagnostic-only"})
	provider := openai.NewProvider("fixture-key", openai.WithBaseURL(server.URL), openai.WithModel("actual-model"), openai.WithResponsesAPI(false), openai.WithHTTPClient(server.Client()))
	sink := &recordedCompactSink{}
	live := recordedAuxiliaryLive(t, settings, provider, engine.WithLiveSink(sink))
	live.RestoreContext(manualHistory())
	reservation, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if _, err := reservation.CompactRecordedSession(t.Context(), "session"); err != nil {
		t.Fatal(err)
	}
	_, batch, err := live.RecordedHistoryWithAccountingBatch("session")
	if err != nil || len(batch.Manual) != 1 {
		t.Fatalf("capture: %v %+v", err, batch)
	}
	record, err := engine.DecodeRecordedSessionManualAccounting(batch.Manual[0].PayloadJSON)
	if err != nil || record.Provider != "actual-route" || record.Model != "actual-model" || !reflect.DeepEqual(record.RateCard, recordedAccountingFixture().RateCard) || record.Usage == nil || record.Usage.PromptTokens != 7 {
		t.Fatalf("actual attribution: %+v %v", record, err)
	}
	if !reflect.DeepEqual(wireModels, []string{"actual-model"}) || !reflect.DeepEqual(sink.labels, []string{"diagnostic-only"}) {
		t.Fatalf("wire=%v diagnostics=%v", wireModels, sink.labels)
	}
}
