package engine_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

const targetModelInfoRows = `{"data":[
{"model_name":"alias","litellm_params":{"model":"vendor/model-a"},"model_info":{"id":"deployment-a","max_input_tokens":1000,"max_output_tokens":200,"input_cost_per_token":0,"supported_endpoints":["/v1/messages"]}},
{"model_name":"alias","litellm_params":{"model":"other/model-b"},"model_info":{"id":"deployment-b","max_output_tokens":300,"output_cost_per_token":0.000002,"supported_endpoints":["/v1/chat/completions"]}},
{"model_name":"other-alias","litellm_params":{"model":"third/model-c"},"model_info":{"id":"deployment-c"}}
]}`

func TestTargetModelInfoOptionalLastGoodAndClone(t *testing.T) {
	t.Parallel()
	var status atomic.Int32
	status.Store(http.StatusForbidden)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/model/info" || r.Header.Get("Authorization") != "Bearer ordinary-test-key" {
			t.Error("metadata lost actual provider scope")
		}
		w.WriteHeader(int(status.Load()))
		if status.Load() == http.StatusOK {
			_, _ = io.WriteString(w, targetModelInfoRows)
		}
	}))
	t.Cleanup(server.Close)
	settings := preparedGatewaySettings(server.URL)
	update, err := settings.PrepareTarget(t.Context(), engine.ProviderSpec{Name: "gateway-route", Model: "alias", APIKey: "ordinary-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int32{http.StatusForbidden, http.StatusNotFound} {
		status.Store(code)
		if _, err := update.Facts.RefreshModelInfo(t.Context()); !errors.Is(err, litellm.ErrModelInfoUnavailable) {
			t.Fatalf("optional %d = %v", code, err)
		}
		if _, err := update.Facts.CachedModelInfo(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
			t.Fatal(err)
		}
	}
	status.Store(http.StatusOK)
	snapshot, err := update.Facts.RefreshModelInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 3 || snapshot.Rows[0].Deployment == snapshot.Rows[1].Deployment || snapshot.Rows[2].RequestedModel != "other-alias" {
		t.Fatal("heterogeneous/duplicate alias rows discarded")
	}
	if snapshot.Rows[0].Limits.InputTokens != 1000 || snapshot.Rows[0].Limits.OutputTokens != 200 || snapshot.Rows[1].Limits.InputTokens != 0 || snapshot.Rows[0].Limits.ContextTokens != 0 {
		t.Fatal("independent advertised ceilings changed")
	}
	if !snapshot.Rows[0].Prices.Input.Known || snapshot.Rows[0].Prices.Output.Known {
		t.Fatal("known zero or absent price changed")
	}
	if snapshot.Rows[0].SupportedProtocols[0] != llm.ModelProtocols.PROTOCOLMESSAGES || update.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatal("declared endpoint changed selected codec")
	}
	if len(update.Facts.Resolution.Descriptor.Surfaces) != 0 || update.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) || update.Window != engine.LiveContextWindow {
		t.Fatal("gateway declaration granted capability, price, or operational budget")
	}
	good := snapshot.Clone()
	snapshot.Rows[0].SupportedProtocols[0] = llm.ModelProtocols.PROTOCOLRESPONSES
	snapshot.Rows[0].RequestedModel = "mutated"
	cached, err := update.Facts.Clone().CachedModelInfo()
	if err != nil || !reflect.DeepEqual(cached, good) {
		t.Fatalf("cache alias = %+v, %v", cached, err)
	}
	for _, code := range []int32{http.StatusForbidden, http.StatusNotFound} {
		status.Store(code)
		if _, err := update.Facts.RefreshModelInfo(t.Context()); !errors.Is(err, litellm.ErrModelInfoUnavailable) {
			t.Fatal(err)
		}
		cached, err = update.Facts.CachedModelInfo()
		if err != nil || !reflect.DeepEqual(cached, good) {
			t.Fatal("failed refresh changed last-good or extended expiry")
		}
	}
	// At a consumer's later clock this remains stale, not renewed by a denial.
	at := good.Provenance.ExpiresAt.Add(time.Second)
	if cached.Provenance.ExpiresAt.IsZero() || at.Before(cached.Provenance.ExpiresAt) {
		t.Fatal("stale observation lost expiry")
	}
}

func TestTargetModelInfoOldCompletionCannotEnrichNewGeneration(t *testing.T) {
	t.Parallel()
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer old-test-key" {
			close(started)
			<-release
		}
		_, _ = io.WriteString(w, targetModelInfoRows)
	}))
	t.Cleanup(server.Close)
	settings := preparedGatewaySettings(server.URL)
	spec := engine.ProviderSpec{Name: "gateway-route", Model: "alias", APIKey: "old-test-key"}
	old, err := settings.PrepareTargetCached(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, refreshErr := old.Facts.RefreshModelInfo(t.Context()); done <- refreshErr }()
	<-started
	spec.APIKey = "new-test-key"
	next, err := settings.PrepareTargetCached(t.Context(), spec)
	if err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	live := reservationRunner(t, next.Provider)
	live.ApplyTarget(next)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	active := live.RunTarget()
	if active.Facts.Generation != next.Facts.Generation || active.Facts.Generation == old.Facts.Generation {
		t.Fatal("generation changed after old result")
	}
	if _, err := active.Facts.CachedModelInfo(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
		t.Fatalf("old result leaked to new credential scope: %v", err)
	}
	if _, err := active.Facts.RefreshModelInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Facts.CachedModelInfo(); err != nil {
		t.Fatal("same generation failed to share cache", err)
	}
}

func TestTargetModelInfoRefreshUsesCallerCancellation(t *testing.T) {
	t.Parallel()
	settings := preparedGatewaySettings("http://127.0.0.1:1")
	update, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", Model: "alias", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := update.Facts.RefreshModelInfo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity = %v", err)
	}
	if _, err := update.Facts.CachedModelInfo(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
		t.Fatal("canceled refresh populated cache", err)
	}
}
