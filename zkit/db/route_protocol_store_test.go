package db_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func openRouteProtocolStore(t *testing.T, path string) *db.Store {
	t.Helper()
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func assertStoredRouteProtocol(t *testing.T, store *db.Store, provider, alias string, want llm.ModelProtocol) {
	t.Helper()
	got, err := store.GetRouteProtocol(t.Context(), provider, alias)
	if err != nil || got != want {
		t.Fatalf("GetRouteProtocol(%q, %q) = %s, %v; want %s", provider, alias, got, err, want)
	}
}

func TestRouteProtocolStoreDurabilityAndExactKeys(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store := openRouteProtocolStore(t, path)
	const alias = "  Gpt/Production Ω模型  "
	for _, protocol := range []llm.ModelProtocol{
		llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES,
		llm.ModelProtocols.PROTOCOLGENERATECONTENT, llm.ModelProtocols.PROTOCOLMESSAGES,
	} {
		if err := store.SetRouteProtocol(t.Context(), "litellm", alias, protocol); err != nil {
			t.Fatal(err)
		}
		assertStoredRouteProtocol(t, store, "litellm", alias, protocol)
	}
	for _, key := range []struct{ provider, alias string }{
		{"gateway-two", alias}, {"litellm", "  gpt/Production Ω模型  "},
		{"litellm", "Gpt/Production Ω模型"}, {"litellm", "\x00/opaque"},
	} {
		if err := store.SetRouteProtocol(t.Context(), key.provider, key.alias, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openRouteProtocolStore(t, path)
	assertStoredRouteProtocol(t, store, "litellm", alias, llm.ModelProtocols.PROTOCOLMESSAGES)
	assertStoredRouteProtocol(t, store, "gateway-two", alias, llm.ModelProtocols.PROTOCOLRESPONSES)
	assertStoredRouteProtocol(t, store, "litellm", "\x00/opaque", llm.ModelProtocols.PROTOCOLRESPONSES)
	for range 2 {
		if err := store.DeleteRouteProtocol(t.Context(), "litellm", alias); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openRouteProtocolStore(t, path)
	if _, err := store.GetRouteProtocol(t.Context(), "litellm", alias); !errors.Is(err, backends.ErrRouteProtocolNotFound) {
		t.Fatalf("deleted policy = %v", err)
	}
	assertStoredRouteProtocol(t, store, "gateway-two", alias, llm.ModelProtocols.PROTOCOLRESPONSES)
	assertStoredRouteProtocol(t, store, "litellm", "  gpt/Production Ω模型  ", llm.ModelProtocols.PROTOCOLRESPONSES)
	assertStoredRouteProtocol(t, store, "litellm", "Gpt/Production Ω模型", llm.ModelProtocols.PROTOCOLRESPONSES)
}

func TestRouteProtocolStoreRejectsInvalidRepresentations(t *testing.T) {
	t.Parallel()
	store := openRouteProtocolStore(t, filepath.Join(t.TempDir(), "state.db"))
	for _, key := range []struct{ provider, alias string }{{"", "alias"}, {"litellm", ""}, {"\xff", "alias"}, {"litellm", "\xff"}} {
		if err := store.SetRouteProtocol(t.Context(), key.provider, key.alias, llm.ModelProtocols.PROTOCOLMESSAGES); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid set key = %v", err)
		}
		if _, err := store.GetRouteProtocol(t.Context(), key.provider, key.alias); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid get key = %v", err)
		}
		if err := store.DeleteRouteProtocol(t.Context(), key.provider, key.alias); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid delete key = %v", err)
		}
	}
	invalid, _ := llm.ParseModelProtocol("invalid")
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLUNKNOWN, invalid} {
		if err := store.SetRouteProtocol(t.Context(), "litellm", "alias", protocol); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("invalid set protocol = %v", err)
		}
	}
	for _, row := range []struct{ provider, alias, protocol string }{
		{"", "alias", "messages"}, {"litellm", "", "messages"},
		{"litellm", "alias", "unknown"}, {"litellm", "alias", "anthropic_messages"},
	} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO llm_route_protocols VALUES (?, ?, ?)", row.provider, row.alias, row.protocol); err == nil {
			t.Fatalf("schema accepted invalid row: %#v", row)
		}
	}
	// Simulate an externally damaged database, bypassing CHECK only in this fixture.
	if _, err := store.DB().ExecContext(t.Context(), "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"unknown", "invalid", "anthropic_messages", "Messages", ""} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT OR REPLACE INTO llm_route_protocols VALUES ('litellm', 'alias', ?)", raw); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetRouteProtocol(t.Context(), "litellm", "alias"); !errors.Is(err, backends.ErrInvalidProviderDef) {
			t.Fatalf("damaged stored protocol %q = %v", raw, err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), "PRAGMA ignore_check_constraints = OFF"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRouteProtocol(t.Context(), "litellm", "alias"); err == nil || errors.Is(err, backends.ErrRouteProtocolNotFound) {
		t.Fatalf("closed store read = %v", err)
	}
}

func TestRouteProtocolStoreRedactsMalformedStoredProtocol(t *testing.T) {
	t.Parallel()
	store := openRouteProtocolStore(t, filepath.Join(t.TempDir(), "state.db"))
	const raw = "protocol-secret-canary\nAuthorization: Bearer credential-secret-canary\nprivate-route-canary"
	// Simulate external corruption, bypassing the schema's protocol constraint.
	if _, err := store.DB().ExecContext(t.Context(), "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO llm_route_protocols VALUES ('litellm', 'alias', ?)", raw); err != nil {
		t.Fatal(err)
	}
	_, err := store.GetRouteProtocol(t.Context(), "litellm", "alias")
	if !errors.Is(err, backends.ErrInvalidProviderDef) {
		t.Fatal("malformed stored protocol must return ErrInvalidProviderDef")
	}
	// Inspect all exposed causes too, including multi-wrapped errors. Checking
	// individual canaries catches quoted/escaped multiline parser diagnostics.
	pending := []error{err}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for _, canary := range []string{raw, "protocol-secret-canary", "credential-secret-canary", "private-route-canary"} {
			if strings.Contains(current.Error(), canary) {
				t.Fatal("stored protocol content leaked through the returned error chain")
			}
		}
		switch cause := current.(type) { //nolint:errorlint // Inspect this node's unwrap method, not a matching descendant.
		case interface{ Unwrap() []error }:
			pending = append(pending, cause.Unwrap()...)
		case interface{ Unwrap() error }:
			if next := cause.Unwrap(); next != nil {
				pending = append(pending, next)
			}
		}
	}
}

func TestRouteProtocolProviderEditDeleteAtomicity(t *testing.T) {
	t.Parallel()
	store := openRouteProtocolStore(t, filepath.Join(t.TempDir(), "state.db"))
	provider := backends.StoredProvider{Name: "gateway", AdapterType: backends.AdapterTypes.LITELLM.String(), Enabled: true}
	if err := store.UpsertProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"one", "two"} {
		if err := store.SetRouteProtocol(t.Context(), provider.Name, alias, llm.ModelProtocols.PROTOCOLMESSAGES); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetRouteProtocol(t.Context(), "other", "one", llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
		t.Fatal(err)
	}
	provider.DisplayName = "edited"
	if err := store.UpsertProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	assertStoredRouteProtocol(t, store, "gateway", "one", llm.ModelProtocols.PROTOCOLMESSAGES)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_provider_delete BEFORE DELETE ON llm_providers BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProvider(t.Context(), "gateway"); err == nil {
		t.Fatal("expected rejected delete")
	}
	assertStoredRouteProtocol(t, store, "gateway", "one", llm.ModelProtocols.PROTOCOLMESSAGES)
	assertStoredRouteProtocol(t, store, "gateway", "two", llm.ModelProtocols.PROTOCOLMESSAGES)
	if got, err := store.GetProvider(t.Context(), "gateway"); err != nil || got.DisplayName != "edited" {
		t.Fatalf("rollback provider = %#v, %v", got, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_provider_delete"); err != nil {
		t.Fatal(err)
	}
	// Borrowing an existing transaction must neither deadlock nor split cleanup.
	if err := store.WithTx(t.Context(), func(tx *db.Store) error { return tx.DeleteProvider(t.Context(), "gateway") }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProvider(t.Context(), "gateway"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("deleted provider = %v", err)
	}
	for _, alias := range []string{"one", "two"} {
		if _, err := store.GetRouteProtocol(t.Context(), "gateway", alias); !errors.Is(err, backends.ErrRouteProtocolNotFound) {
			t.Fatalf("deleted provider policy = %v", err)
		}
	}
	assertStoredRouteProtocol(t, store, "other", "one", llm.ModelProtocols.PROTOCOLRESPONSES)
}

func TestRouteProtocolLegacyMigration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	migrator, err := migrations.NewProvider(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.UpTo(t.Context(), 35); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(t.Context(), `INSERT INTO llm_providers (name, adapter_type, default_model, created_at, updated_at) VALUES ('gateway', 'litellm', 'opaque alias', 1, 2);
		INSERT INTO settings (workspace, key, value, updated_at) VALUES ('/project', 'model', 'opaque alias', 3)`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store := openRouteProtocolStore(t, path)
	if got, err := store.GetProvider(t.Context(), "gateway"); err != nil || got.DefaultModel != "opaque alias" || got.CreatedAt != 1 || got.UpdatedAt != 2 {
		t.Fatalf("migrated provider = %#v, %v", got, err)
	}
	if got, err := store.GetSettingExact(t.Context(), "/project", "model"); err != nil || got != "opaque alias" {
		t.Fatalf("migrated setting = %q, %v", got, err)
	}
	if _, err := store.GetRouteProtocol(t.Context(), "gateway", "opaque alias"); !errors.Is(err, backends.ErrRouteProtocolNotFound) {
		t.Fatalf("migration invented policy: %v", err)
	}
	reg := backends.NewRegistry(backends.WithStore(store), backends.WithRouteProtocolStore(store))
	if err := reg.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	// No backfill: a legacy gateway still builds using the Chat default.
	if _, err := reg.BuildWithConfig(t.Context(), "gateway", backends.BuildConfig{APIKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
}
