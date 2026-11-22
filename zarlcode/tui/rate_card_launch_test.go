package tui_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/options"
)

const launchCardAlias = " Gateway/模型 Production Alias "

func launchCardData(t *testing.T, target engine.RateCardTarget, revision string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"version": 1,
		"target":  map[string]any{"provider": target.Provider, "model": target.Model, "base_url": target.BaseURL, "protocol": target.Protocol.String()},
		"card": map[string]any{
			"id": "private-local-policy", "revision": revision, "entry": "entry",
			"pricing_provider": "operator", "currency": "EUR", "billing": "token-metered", "applicable": true, "token_only": true,
			"fresh_input_per_million": 0, "cache_read_per_million": 0, "output_per_million": 0,
			"cache_writes": []any{map[string]any{"lifetime_seconds": 300, "per_million": 0}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func launchCardFile(t *testing.T, target engine.RateCardTarget, revision string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "explicit policy.json")
	if err := os.WriteFile(path, launchCardData(t, target, revision), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func launchCardSettings(t *testing.T, endpoint string) *engine.Settings {
	t.Helper()
	settings := newTestSettings(t)
	def, _ := backends.Builtin("litellm")
	def.BaseURL, def.DefaultModel, def.ContextWindow = endpoint, launchCardAlias, 32768
	settings.Registry = backends.NewRegistry(backends.WithRouteProtocolStore(settings.Store), backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
	return settings
}

func launchCardLive(t *testing.T, settings *engine.Settings, target engine.TargetUpdate, opts ...options.Option[engine.LiveRunner]) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(settings.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	opts = append(opts, engine.WithSettings(settings))
	live := engine.NewLiveRunner(target.Provider, ws, target.Spec.Model, opts...)
	testCtx := t.Context()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	live.ApplyTarget(target)
	return live
}

func TestRateCardLaunchActualPreparedSelection(t *testing.T) {
	t.Parallel()
	for _, headless := range []bool{false, true} {
		for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLRESPONSES} {
			t.Run(protocol.String()+map[bool]string{false: "/cached", true: "/headless"}[headless], func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
				t.Cleanup(server.Close)
				settings := launchCardSettings(t, server.URL)
				selected := protocol
				if selected == llm.ModelProtocols.PROTOCOLUNKNOWN {
					selected = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
				}
				selector := engine.RateCardTarget{Provider: "litellm", Model: launchCardAlias, Protocol: selected}
				path := launchCardFile(t, selector, "v1")
				before, err := settings.Store.EffectiveSettings(t.Context(), settings.WorkspaceRoot())
				if err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				target, err := (tui.Launch{Headless: headless, RateCardFile: path}).PrepareTarget(t.Context(), settings, engine.ProviderSpec{Name: "litellm", APIKey: "loopback-only", Protocol: protocol})
				if err != nil {
					t.Fatal(err)
				}
				card := target.RateCard
				if target.Spec.Model != launchCardAlias || target.Spec.Protocol != protocol || target.Facts.SelectedProtocol != selected || target.Spec.BaseURL != "" || card == nil || card.Currency != "EUR" || !card.FreshInput.Known || card.FreshInput.PerMillionTokens != 0 || target.Facts.Resolution.Descriptor.RateCard != card.Reference {
					t.Fatal("launch did not bind exact registered default alias and actual protocol")
				}
				digest := sha256.Sum256(launchCardData(t, selector, "v1"))
				if card.Provenance.Revision != hex.EncodeToString(digest[:]) || card.Provenance.ObservedAt.Before(started) || card.Provenance.ObservedAt.After(time.Now()) {
					t.Fatal("load observation time/content revision lost")
				}
				after, err := settings.Store.EffectiveSettings(t.Context(), settings.WorkspaceRoot())
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("runtime input changed persisted preferences", err)
				}
				plain, err := settings.PrepareTargetCached(t.Context(), target.Spec)
				if err != nil || plain.RateCard != nil || requests.Load() != 0 {
					t.Fatal("launch persisted policy or contacted gateway", err)
				}
				facts, err := json.Marshal(target.Facts) //nolint:musttag // Check the actual public snapshot for private construction data.
				if err != nil || strings.Contains(string(facts), "loopback-only") || strings.Contains(string(facts), server.URL) {
					t.Fatal("private construction details published", err)
				}
			})
		}
	}
}

func TestRateCardLaunchMismatchPreservesTarget(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	settings := launchCardSettings(t, server.URL)
	spec := engine.ProviderSpec{Name: "litellm", APIKey: "loopback-only"}
	selector := engine.RateCardTarget{Provider: "litellm", Model: launchCardAlias, Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}
	initial, err := (tui.Launch{RateCardFile: launchCardFile(t, selector, "initial")}).PrepareTarget(t.Context(), settings, spec)
	if err != nil {
		t.Fatal(err)
	}
	live := launchCardLive(t, settings, initial)
	before := live.RunTarget()
	for _, tc := range []struct {
		name   string
		change func(*engine.RateCardTarget)
	}{
		{"provider", func(v *engine.RateCardTarget) { v.Provider = "other" }},
		{"model", func(v *engine.RateCardTarget) { v.Model = "Gateway/模型 Production Alias" }},
		{"URL is not wildcard", func(v *engine.RateCardTarget) { v.BaseURL = server.URL }},
		{"protocol intent is not actual", func(v *engine.RateCardTarget) { v.Protocol = llm.ModelProtocols.PROTOCOLRESPONSES }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrong := selector
			tc.change(&wrong)
			got, err := (tui.Launch{RateCardFile: launchCardFile(t, wrong, "wrong")}).PrepareTarget(t.Context(), settings, spec)
			if !errors.Is(err, engine.ErrRateCardTargetMismatch) || got.Provider != nil || got.RateCard != nil || !reflect.DeepEqual(before, live.RunTarget()) {
				t.Fatal("mismatch published or altered active target", err)
			}
		})
	}
	_, err = (tui.Launch{RateCardFile: launchCardFile(t, selector, "native")}).PrepareTarget(t.Context(), &engine.Settings{Registry: backends.NewRegistry()}, engine.ProviderSpec{Name: "openai", Model: "native", APIKey: "loopback-only"})
	if !errors.Is(err, engine.ErrRateCardTargetMismatch) || requests.Load() != 0 {
		t.Fatal("unknown native protocol guessed or mismatch contacted gateway", err)
	}
}

func TestRateCardLaunchFileBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, data string
		size       int
	}{
		{name: "malformed", data: `{"private-canary":`},
		{name: "oversized", size: engine.MaxRateCardConfigBytes + 1},
		{name: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "private-path-canary")
			data := []byte(tc.data)
			if tc.size > 0 {
				data = make([]byte, tc.size)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := (tui.Launch{RateCardFile: path}).PrepareTarget(t.Context(), nil, engine.ProviderSpec{})
			assertLaunchFileError(t, err)
		})
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "private-path-canary"), t.TempDir(), os.DevNull} {
		_, err := (tui.Launch{RateCardFile: path}).PrepareTarget(t.Context(), nil, engine.ProviderSpec{})
		assertLaunchFileError(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := (tui.Launch{RateCardFile: "private-path-canary"}).PrepareTarget(ctx, nil, engine.ProviderSpec{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func assertLaunchFileError(t *testing.T, err error) {
	t.Helper()
	var pathErr *os.PathError
	var syntaxErr *json.SyntaxError
	if !errors.Is(err, engine.ErrInvalidRateCardConfig) || errors.As(err, &pathErr) || errors.As(err, &syntaxErr) || strings.Contains(err.Error(), "canary") {
		t.Fatalf("unsafe or missing boundary error: %v", err)
	}
}

func TestRateCardLaunchStartupRebindAndManualClear(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "rebind same parsed basis", true: "changed protocol leaves old target"}[mismatch], func(t *testing.T) {
			settings := launchCardSettings(t, "http://127.0.0.1:1")
			selector := engine.RateCardTarget{Provider: "litellm", Model: launchCardAlias, Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}
			path := launchCardFile(t, selector, "v1")
			initial, cmd, err := (tui.Launch{RateCardFile: path}).PrepareTargetWithStartup(t.Context(), settings, engine.ProviderSpec{Name: "litellm", APIKey: "loopback-only"}, 0)
			if err != nil || cmd == nil {
				t.Fatal("startup preparation", err)
			}
			live := launchCardLive(t, settings, initial)
			ui := tui.New()
			ui.SetSettings(settings)
			ui.SetLiveRunner(live)
			ui.SetProviderContext(initial.Spec, initial.Spec)
			before := live.RunTarget()
			if err := os.WriteFile(path, launchCardData(t, selector, "v2"), 0o600); err != nil {
				t.Fatal(err)
			}
			if mismatch {
				if err := settings.Registry.SetRouteProtocol(t.Context(), "litellm", launchCardAlias, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = ui.Update(cmd())
			after := live.RunTarget()
			if mismatch {
				if !reflect.DeepEqual(before, after) {
					t.Fatal("advisory mismatch dropped old basis")
				}
			} else if after.Facts.Generation == before.Facts.Generation || !reflect.DeepEqual(after.RateCard, before.RateCard) || after.Facts.Resolution.Descriptor.RateCard != before.RateCard.Reference {
				t.Fatal("startup reused old construction, reread changed file, or lost binding")
			}
			runSettingsCloseCmd(t, ui, ui.SwitchTarget(prefs.ModelSelection{Provider: "litellm", Model: "manual/Alias"}))
			if got := live.RunTarget(); got.Model != "manual/Alias" || got.RateCard != nil || got.Facts.Resolution.Descriptor.RateCard != (llm.RateCardReference{}) {
				t.Fatal("manual replacement inherited initial input")
			}
		})
	}
}

type launchCardSink struct {
	runner.NopSink
	mu     sync.Mutex
	events []runner.ProviderAttemptSettled
}

func (*launchCardSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*launchCardSink) PlanUpdated(string, code.Plan)    {}
func (s *launchCardSink) OnProviderAttemptSettled(_ context.Context, event runner.ProviderAttemptSettled) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func TestRateCardLaunchActualDispatchFreezesFileBasis(t *testing.T) {
	for _, headless := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded live", true: "headless"}[headless], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if strings.Contains(string(body), "private-local-policy") || strings.Contains(string(body), "rate_card") {
					t.Error("policy transmitted")
				}
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+`{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			t.Cleanup(unblock)
			settings := launchCardSettings(t, server.URL)
			selector := engine.RateCardTarget{Provider: "litellm", Model: launchCardAlias, Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}
			path := launchCardFile(t, selector, "v1")
			initial, err := (tui.Launch{Headless: headless, RateCardFile: path}).PrepareTarget(t.Context(), settings, engine.ProviderSpec{Name: "litellm", APIKey: "loopback-only"})
			if err != nil {
				t.Fatal(err)
			}
			want := initial.RateCard.Clone()
			sink := &launchCardSink{}
			live := launchCardLive(t, settings, initial, engine.WithLiveSink(sink))
			live.SetLimits(1024, 1, 1, 0)
			if err := settings.Store.SaveActiveSession(t.Context(), db.SessionRecord{ID: "rate-card-session", Workspace: settings.WorkspaceRoot()}); err != nil {
				t.Fatal(err)
			}
			ref, err := settings.Store.CaptureSessionHistory(t.Context(), "rate-card-session", nil, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := settings.Store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{SessionID: "rate-card-session", ID: "initial", SourceSessionID: "rate-card-session", Workspace: settings.WorkspaceRoot(), BoundaryID: "prompt", FormatVersion: 2, Payload: []byte(`{}`), History: ref}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			done := make(chan runner.TaskResult, 1)
			go func() {
				if headless {
					done <- live.RunHeadless(ctx, "respond", 1)
					return
				}
				done <- runner.TaskResult{Err: live.RunRecordedTurn(ctx, "respond", nil, settings.Store, "rate-card-session")}
			}()
			joined := false
			defer func() {
				cancel()
				unblock()
				if !joined {
					<-done
				}
			}()
			select {
			case <-started:
			case early := <-done:
				joined = true
				t.Fatalf("ended before HTTP: %v", early.Err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := os.WriteFile(path, launchCardData(t, selector, "v2"), 0o600); err != nil {
				t.Fatal(err)
			}
			initial.RateCard.Reference.Revision = "caller"
			initial.RateCard.CacheWrites[0].Rate.PerMillionTokens = 99
			capture := live.RunTarget()
			capture.RateCard.Reference.Revision = "snapshot"
			capture.RateCard.CacheWrites[0].Rate.PerMillionTokens = 88
			unblock()
			result := <-done
			joined = true
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			if len(sink.events) != 1 || !reflect.DeepEqual(sink.events[0].RateCard, &want) {
				t.Fatal("actual production dispatch lost frozen launch basis")
			}
			if headless && (len(result.ProviderObservations) != 1 || !reflect.DeepEqual(result.ProviderObservations[0].RateCard, &want)) {
				t.Fatal("headless result lost launch basis")
			}
		})
	}
}

func TestRateCardLaunchStoredProtocolAndLiteralPaths(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	settings := launchCardSettings(t, server.URL)
	if err := settings.Registry.SetRouteProtocol(t.Context(), "litellm", launchCardAlias, llm.ModelProtocols.PROTOCOLMESSAGES); err != nil {
		t.Fatal(err)
	}
	spec := engine.ProviderSpec{Name: "litellm", BaseURL: server.URL + "/Exact/", APIKey: "loopback-only"}
	selector := engine.RateCardTarget{Provider: "litellm", Model: launchCardAlias, BaseURL: spec.BaseURL, Protocol: llm.ModelProtocols.PROTOCOLMESSAGES}
	path := filepath.Join(t.TempDir(), "~literal policy.json")
	// Exactly 64 KiB is accepted; the loader's +1 read distinguishes it from
	// oversized input without truncating the parser's complete representation.
	wire := launchCardData(t, selector, "literal")
	wire = append(wire, []byte(strings.Repeat(" ", engine.MaxRateCardConfigBytes-len(wire)))...)
	if err := os.WriteFile(path, wire, 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (tui.Launch{RateCardFile: relative}).PrepareTarget(t.Context(), settings, spec)
	if err != nil || prepared.RateCard.Reference.Revision != "literal" || prepared.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || prepared.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLMESSAGES || calls.Load() != 0 {
		t.Fatal("literal relative path or actual stored protocol lost", err)
	}
	link := filepath.Join(t.TempDir(), "symlink.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linked, err := (tui.Launch{RateCardFile: link}).PrepareTarget(t.Context(), settings, spec)
	if err != nil || linked.RateCard.Provenance.Revision != prepared.RateCard.Provenance.Revision {
		t.Fatal("standard symlink-to-regular-file semantics lost", err)
	}
}
