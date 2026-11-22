package backends_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func routeProtocolRegistry(t *testing.T) *backends.ProviderRegistry {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	def, _ := backends.Builtin("litellm")
	def.DefaultModel = " default/Ω alias "
	return backends.NewRegistry(backends.WithRouteProtocolStore(store), backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
}

func builtRouteProtocol(t *testing.T, reg *backends.ProviderRegistry, model string, override llm.ModelProtocol) llm.ModelProtocol {
	t.Helper()
	provider, err := reg.BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{Model: model, APIKey: "fixture", Protocol: override})
	if err != nil {
		t.Fatal(err)
	}
	gateway, ok := provider.(*litellm.Provider)
	if !ok {
		t.Fatalf("built provider type = %T", provider)
	}
	return gateway.Protocol()
}

func TestRouteProtocolRegistryPrecedence(t *testing.T) {
	reg := routeProtocolRegistry(t)
	const first = " default/Ω alias "
	const second = " Responses/Unlisted alias "
	for _, row := range []struct {
		alias    string
		protocol llm.ModelProtocol
	}{{first, llm.ModelProtocols.PROTOCOLMESSAGES}, {second, llm.ModelProtocols.PROTOCOLRESPONSES}, {"google", llm.ModelProtocols.PROTOCOLGENERATECONTENT}} {
		if err := reg.SetRouteProtocol(t.Context(), "litellm", row.alias, row.protocol); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, alias    string
		override, want llm.ModelProtocol
	}{
		{"default resolved before policy", "", llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLMESSAGES},
		{"exact first alias", first, llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLMESSAGES},
		{"exact second alias", second, llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLRESPONSES},
		{"generate content", "google", llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLGENERATECONTENT},
		{"override messages", first, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
		{"override responses", second, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
		{"absent", "unconfigured", llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
		{"no whitespace normalization", "default/Ω alias", llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
		{"no case normalization", " responses/Unlisted alias ", llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := builtRouteProtocol(t, reg, tc.alias, tc.override); got != tc.want {
				t.Fatalf("protocol = %s, want %s", got, tc.want)
			}
		})
	}
	if got, err := reg.GetRouteProtocol(t.Context(), "litellm", first); err != nil || got != llm.ModelProtocols.PROTOCOLMESSAGES {
		t.Fatalf("override changed stored policy: %s, %v", got, err)
	}
	if got, err := reg.GetRouteProtocol(t.Context(), "litellm", second); err != nil || got != llm.ModelProtocols.PROTOCOLRESPONSES {
		t.Fatalf("override changed stored policy: %s, %v", got, err)
	}
	old, err := reg.BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{Model: first, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRouteProtocol(t.Context(), "litellm", first, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
		t.Fatal(err)
	}
	if old.(*litellm.Provider).Protocol() != llm.ModelProtocols.PROTOCOLMESSAGES || builtRouteProtocol(t, reg, first, llm.ModelProtocols.PROTOCOLUNKNOWN) != llm.ModelProtocols.PROTOCOLRESPONSES {
		t.Fatal("policy update changed existing provider or did not affect future build")
	}
	for range 2 {
		if err := reg.DeleteRouteProtocol(t.Context(), "litellm", first); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.GetRouteProtocol(t.Context(), "litellm", first); !errors.Is(err, backends.ErrRouteProtocolNotFound) {
		t.Fatalf("absent policy = %v", err)
	}
	if got := builtRouteProtocol(t, reg, first, llm.ModelProtocols.PROTOCOLUNKNOWN); got != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatalf("deleted policy resolved to %s", got)
	}
}

// routeProtocolReadResult models a storage read result; writes are unsupported.
type routeProtocolReadResult struct {
	protocol llm.ModelProtocol
	err      error
}

func (s routeProtocolReadResult) GetRouteProtocol(context.Context, string, string) (llm.ModelProtocol, error) {
	return s.protocol, s.err
}
func (routeProtocolReadResult) SetRouteProtocol(context.Context, string, string, llm.ModelProtocol) error {
	return errors.ErrUnsupported
}
func (routeProtocolReadResult) DeleteRouteProtocol(context.Context, string, string) error {
	return errors.ErrUnsupported
}

func TestRouteProtocolRegistryStorageFailures(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("storage unavailable")
	invalid, _ := llm.ParseModelProtocol("invalid")
	for _, tc := range []struct {
		name   string
		result routeProtocolReadResult
		want   error
	}{
		{"read failure", routeProtocolReadResult{err: storageErr}, storageErr},
		{"no store from configured adapter is not absence", routeProtocolReadResult{err: backends.ErrNoStore}, backends.ErrNoStore},
		{"unknown stored protocol", routeProtocolReadResult{}, backends.ErrInvalidProviderDef},
		{"invalid stored protocol", routeProtocolReadResult{protocol: invalid}, backends.ErrInvalidProviderDef},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := backends.NewRegistry(backends.WithRouteProtocolStore(tc.result))
			if _, err := reg.BuildWithConfig(t.Context(), "litellm", backends.BuildConfig{Model: "alias", APIKey: "fixture"}); !errors.Is(err, tc.want) {
				t.Fatalf("build storage error = %v, want %v", err, tc.want)
			}
			// Explicit runtime Chat must skip even a failing/corrupt policy read.
			if got := builtRouteProtocol(t, reg, "alias", llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS); got != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
				t.Fatalf("override protocol = %s", got)
			}
		})
	}
	reg := backends.NewRegistry(backends.WithRouteProtocolStore(routeProtocolReadResult{err: backends.ErrRouteProtocolNotFound}))
	if got := builtRouteProtocol(t, reg, "alias", llm.ModelProtocols.PROTOCOLUNKNOWN); got != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatalf("semantic absence = %s", got)
	}
	if err := reg.SetRouteProtocol(t.Context(), "litellm", "alias", llm.ModelProtocols.PROTOCOLMESSAGES); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("write failure = %v", err)
	}
	if err := reg.DeleteRouteProtocol(t.Context(), "litellm", "alias"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("delete failure = %v", err)
	}
}

func TestRouteProtocolRegistryBoundaries(t *testing.T) {
	t.Parallel()
	without := backends.NewRegistry()
	if _, err := without.GetRouteProtocol(t.Context(), "litellm", "alias"); !errors.Is(err, backends.ErrNoStore) {
		t.Fatalf("get without store = %v", err)
	}
	if err := without.SetRouteProtocol(t.Context(), "litellm", "alias", llm.ModelProtocols.PROTOCOLMESSAGES); !errors.Is(err, backends.ErrNoStore) {
		t.Fatalf("set without store = %v", err)
	}
	if err := without.DeleteRouteProtocol(t.Context(), "litellm", "alias"); !errors.Is(err, backends.ErrNoStore) {
		t.Fatalf("delete without store = %v", err)
	}
	if got := builtRouteProtocol(t, without, "alias", llm.ModelProtocols.PROTOCOLUNKNOWN); got != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatalf("optional storage default = %s", got)
	}
	reg := backends.NewRegistry(backends.WithRouteProtocolStore(routeProtocolReadResult{}))
	for _, key := range []struct{ provider, alias string }{{"openai", "alias"}, {"", "alias"}, {"litellm", ""}, {"litellm", "\xff"}, {"\xff", "alias"}} {
		if _, err := reg.GetRouteProtocol(t.Context(), key.provider, key.alias); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid get = %v", err)
		}
		if err := reg.SetRouteProtocol(t.Context(), key.provider, key.alias, llm.ModelProtocols.PROTOCOLMESSAGES); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid set = %v", err)
		}
		if err := reg.DeleteRouteProtocol(t.Context(), key.provider, key.alias); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid delete = %v", err)
		}
	}
	invalid, _ := llm.ParseModelProtocol("invalid")
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLUNKNOWN, invalid} {
		if err := reg.SetRouteProtocol(t.Context(), "litellm", "alias", protocol); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid protocol set = %v", err)
		}
	}
}
