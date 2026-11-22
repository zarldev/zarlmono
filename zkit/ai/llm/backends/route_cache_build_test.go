package backends_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

type constructionCacheStore struct {
	policy          backends.RouteCachePolicy
	err             error
	reads           int
	provider, alias string
}

func (s *constructionCacheStore) GetRouteCachePolicy(_ context.Context, provider, alias string) (backends.RouteCachePolicy, error) {
	s.reads++
	s.provider, s.alias = provider, alias
	return s.policy, s.err
}
func (s *constructionCacheStore) SetRouteCachePolicy(context.Context, string, string, backends.RouteCachePolicy) error {
	return nil
}
func (s *constructionCacheStore) DeleteRouteCachePolicy(context.Context, string, string) error {
	return nil
}

func TestBuildResolvedCacheResolution(t *testing.T) {
	alias := " opaque/ALIAS  "
	saved := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, System: true, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H}
	for _, tc := range []struct {
		name              string
		intent, stored    backends.RouteCachePolicy
		storeErr, wantErr error
		want              backends.RouteCachePolicy
		reads             int
	}{
		{name: "missing", storeErr: backends.ErrRouteCachePolicyNotFound, want: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}, reads: 1},
		{name: "saved", stored: saved, want: saved, reads: 1},
		{name: "mask", intent: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}, stored: saved, want: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}},
		{name: "override", intent: saved, storeErr: errors.New("not consulted"), want: saved},
		{name: "error", storeErr: errors.New("storage"), wantErr: errors.New("storage"), reads: 1},
		{name: "corrupt", stored: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEINHERIT}, wantErr: backends.ErrInvalidRouteCachePolicy, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &constructionCacheStore{policy: tc.stored, err: tc.storeErr}
			reg := backends.NewRegistry(backends.WithRouteCachePolicyStore(store))
			built, err := reg.BuildResolved(t.Context(), "litellm", backends.BuildConfig{Model: alias, APIKey: "fake", CachePolicy: tc.intent})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) && (tc.name != "error" || !strings.Contains(err.Error(), "storage")) {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if store.reads != tc.reads {
				t.Fatalf("reads=%d want=%d", store.reads, tc.reads)
			}
			if store.reads > 0 && (store.provider != "litellm" || store.alias != alias) {
				t.Fatalf("key=%q,%q", store.provider, store.alias)
			}
			if err == nil && (built.Model != alias || built.Protocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS || built.CachePolicy != tc.want || built.Provider.Name() != "litellm") {
				t.Fatalf("result=%+v", built)
			}
		})
	}
}

func TestBuildResolvedCacheWire(t *testing.T) {
	explicit := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, System: true, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H}
	for _, tc := range []struct {
		name, provider string
		protocol       llm.ModelProtocol
		policy, want   backends.RouteCachePolicy
		wantMarker     bool
		wantTTL        string
	}{
		{"chat-default", "litellm", llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}, false, ""},
		{"chat-explicit", "litellm", llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, explicit, explicit, true, "1h"},
		{"chat-explicit-default-ttl", "litellm", llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, System: true}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, System: true, StaticTTL: backends.RouteCacheTTLs.CACHETTL5M}, true, "5m"},
		{"messages-default", "litellm", llm.ModelProtocols.PROTOCOLMESSAGES, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}, true, "1h"},
		{"messages-explicit-defaults", "litellm", llm.ModelProtocols.PROTOCOLMESSAGES, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}, true, "1h"},
		{"messages-disabled", "litellm", llm.ModelProtocols.PROTOCOLMESSAGES, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}, false, ""},
		{"anthropic-default", "anthropic", llm.ModelProtocols.PROTOCOLUNKNOWN, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}, true, "1h"},
		{"anthropic-explicit", "anthropic", llm.ModelProtocols.PROTOCOLUNKNOWN, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL5M}, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL5M, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				raw = string(data)
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.protocol == llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}
			}))
			defer server.Close()
			built, err := backends.NewRegistry().BuildResolved(t.Context(), tc.provider, backends.BuildConfig{Model: "opaque alias", BaseURL: server.URL, APIKey: "fake", Protocol: tc.protocol, CachePolicy: tc.policy})
			if err != nil {
				t.Fatal(err)
			}
			if built.CachePolicy != tc.want {
				t.Fatalf("effective policy=%+v want=%+v", built.CachePolicy, tc.want)
			}
			for _, err := range built.Provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleSystem, Content: "stable"}, {Role: llm.RoleUser, Content: "hello"}}, Stream: true}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if raw == "" {
				t.Fatal("no request")
			}
			var body any
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				t.Fatal(err)
			}
			marked := strings.Contains(raw, "cache_control")
			if marked != tc.wantMarker || (tc.wantTTL != "" && !strings.Contains(raw, `"ttl":"`+tc.wantTTL+`"`)) {
				t.Fatalf("marker=%v wire=%s", marked, raw)
			}
		})
	}
}

func TestBuildResolvedRejectsUnsupportedCache(t *testing.T) {
	explicit := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, System: true}
	for _, tc := range []struct {
		provider string
		protocol llm.ModelProtocol
		policy   backends.RouteCachePolicy
	}{
		{"openai", llm.ModelProtocols.PROTOCOLUNKNOWN, explicit},
		{"litellm", llm.ModelProtocols.PROTOCOLRESPONSES, explicit},
		{"litellm", llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT}},
		{"litellm", llm.ModelProtocols.PROTOCOLMESSAGES, explicit},
		{"anthropic", llm.ModelProtocols.PROTOCOLUNKNOWN, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL5M, RollingTTL: backends.RouteCacheTTLs.CACHETTL1H}},
	} {
		_, err := backends.NewRegistry().BuildResolved(t.Context(), tc.provider, backends.BuildConfig{APIKey: "fake", Protocol: tc.protocol, CachePolicy: tc.policy})
		if !errors.Is(err, backends.ErrInvalidRouteCachePolicy) {
			t.Fatalf("%s %s: %v", tc.provider, tc.protocol, err)
		}
	}
}

type constructionProtocolStore struct {
	protocol llm.ModelProtocol
	reads    int
	alias    string
}

func (s *constructionProtocolStore) GetRouteProtocol(_ context.Context, _, alias string) (llm.ModelProtocol, error) {
	s.reads++
	s.alias = alias
	return s.protocol, nil
}
func (*constructionProtocolStore) SetRouteProtocol(context.Context, string, string, llm.ModelProtocol) error {
	return nil
}
func (*constructionProtocolStore) DeleteRouteProtocol(context.Context, string, string) error {
	return nil
}

func TestBuildResolvedProtocolAndSnapshot(t *testing.T) {
	cache := &constructionCacheStore{policy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}}
	protocol := &constructionProtocolStore{protocol: llm.ModelProtocols.PROTOCOLMESSAGES}
	reg := backends.NewRegistry(backends.WithRouteCachePolicyStore(cache), backends.WithRouteProtocolStore(protocol))
	cfg := backends.BuildConfig{Model: " exact/alias ", APIKey: "fake"}
	built, err := reg.BuildResolved(t.Context(), "litellm", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if built.Protocol != llm.ModelProtocols.PROTOCOLMESSAGES || built.CachePolicy != cache.policy || protocol.reads != 1 || cache.reads != 1 || protocol.alias != cfg.Model || cache.alias != cfg.Model {
		t.Fatalf("result=%+v protocol=%+v cache=%+v", built, protocol, cache)
	}
	cache.policy = backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H}
	protocol.protocol = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
	if built.CachePolicy.Mode != backends.RouteCacheModes.CACHEDISABLED || built.Provider.(*litellm.Provider).Protocol() != llm.ModelProtocols.PROTOCOLMESSAGES {
		t.Fatal("built provider changed after store mutation")
	}
	legacy, err := reg.BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{Model: cfg.Model, APIKey: "fake", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, CachePolicy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}})
	if err != nil || legacy.Name() != "litellm" || protocol.reads != 1 || cache.reads != 1 {
		t.Fatalf("compat=%v %v", legacy, err)
	}
}

func TestBuildResolvedLookupKeys(t *testing.T) {
	for _, alias := range []string{"", "\xff"} {
		t.Run("protocol/"+alias, func(t *testing.T) {
			protocol := &constructionProtocolStore{protocol: llm.ModelProtocols.PROTOCOLMESSAGES}
			cache := &constructionCacheStore{}
			reg := backends.NewRegistry(backends.WithRouteProtocolStore(protocol), backends.WithRouteCachePolicyStore(cache))
			_, err := reg.BuildResolved(t.Context(), "litellm", backends.BuildConfig{Model: alias, APIKey: "fake"})
			if !errors.Is(err, backends.ErrInvalidProviderDef) || protocol.reads != 0 || cache.reads != 0 {
				t.Fatalf("lookup key error=%v protocol=%d cache=%d", err, protocol.reads, cache.reads)
			}
			// An explicit protocol did not consult this store in the legacy path.
			_, err = reg.BuildResolved(t.Context(), "litellm", backends.BuildConfig{Model: alias, APIKey: "fake", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, CachePolicy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}})
			if err != nil || protocol.reads != 0 || cache.reads != 0 {
				t.Fatalf("explicit bypass error=%v protocol=%d cache=%d", err, protocol.reads, cache.reads)
			}
		})
	}
	cache := &constructionCacheStore{}
	reg := backends.NewRegistry(backends.WithRouteCachePolicyStore(cache))
	_, err := reg.BuildResolved(t.Context(), "litellm", backends.BuildConfig{Model: "\xff", APIKey: "fake", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS})
	if !errors.Is(err, backends.ErrInvalidRouteCachePolicy) || cache.reads != 0 {
		t.Fatalf("cache lookup key error=%v reads=%d", err, cache.reads)
	}
	// An empty alias cannot be queried and retains the legacy construction fallback.
	built, err := reg.BuildResolved(t.Context(), "litellm", backends.BuildConfig{APIKey: "fake", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS})
	if err != nil || built.CachePolicy.Mode != backends.RouteCacheModes.CACHEDISABLED || cache.reads != 0 {
		t.Fatalf("empty alias fallback=%+v error=%v reads=%d", built, err, cache.reads)
	}
}

func TestBuildResolvedEffectivePolicyRebuild(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, string(data))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	cache := &constructionCacheStore{policy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}}
	protocol := &constructionProtocolStore{protocol: llm.ModelProtocols.PROTOCOLMESSAGES}
	reg := backends.NewRegistry(backends.WithRouteCachePolicyStore(cache), backends.WithRouteProtocolStore(protocol))
	cfg := backends.BuildConfig{Model: "opaque alias", BaseURL: server.URL, APIKey: "fake"}
	first, err := reg.BuildResolved(t.Context(), "litellm", cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}
	if first.CachePolicy != want || cfg.CachePolicy.Mode != backends.RouteCacheModes.CACHEINHERIT {
		t.Fatalf("effective=%+v intent=%+v", first.CachePolicy, cfg.CachePolicy)
	}
	cache.policy = backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}
	protocol.protocol = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
	rebuilt, err := reg.BuildResolved(t.Context(), "litellm", backends.BuildConfig{Model: first.Model, BaseURL: server.URL, APIKey: "fake", Protocol: first.Protocol, CachePolicy: first.CachePolicy})
	if err != nil || rebuilt.CachePolicy != want || rebuilt.Protocol != first.Protocol || rebuilt.Provider.(*litellm.Provider).Protocol() != first.Provider.(*litellm.Provider).Protocol() || cache.reads != 1 || protocol.reads != 1 {
		t.Fatalf("rebuild=%+v error=%v cache reads=%d protocol reads=%d", rebuilt, err, cache.reads, protocol.reads)
	}
	for _, provider := range []llm.Provider{first.Provider, rebuilt.Provider} {
		for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleSystem, Content: "stable"}, {Role: llm.RoleUser, Content: "hello"}}, Stream: true}) {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[0], `"ttl":"1h"`) {
		t.Fatalf("rebuilt wire differs: %q", bodies)
	}
}

func TestBuildResolvedOtherAdapterKeepsDefault(t *testing.T) {
	built, err := backends.NewRegistry().BuildResolved(t.Context(), "openai", backends.BuildConfig{Model: "gpt-4o-mini", APIKey: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if built.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || built.CachePolicy != (backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}) {
		t.Fatalf("other adapter facts=%+v", built)
	}
}
