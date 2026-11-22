package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func TestMigrationProviderAccountingPreservesExistingState(t *testing.T) {
	t.Parallel()

	for _, version := range []int64{35, 36} {
		t.Run(migrationVersionName(version), func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "state.db")
			d, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := migrations.NewProvider(d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.UpTo(ctx, version); err != nil {
				t.Fatalf("migrate to %d: %v", version, err)
			}
			seed := `INSERT INTO sessions (id, workspace, context_json, last_usage_json, created_at, updated_at)
				VALUES ('legacy', '/workspace', '{"messages":["kept"]}', '{"input_tokens":17}', 1, 2);
				INSERT INTO session_history_batches (session_id, batch_id, checksum) VALUES ('legacy', 'receipt', 'digest');
				INSERT INTO llm_providers (name, adapter_type, created_at, updated_at) VALUES ('saved-provider', 'openai', 1, 2);
				INSERT INTO settings (workspace, key, value, updated_at) VALUES ('/workspace', 'provider.endpoint', 'https://example.invalid', 2);`
			if version == 36 {
				seed += ` INSERT INTO llm_route_protocols (provider, alias, protocol) VALUES ('saved-provider', 'chat', 'responses');`
			}
			if _, err := d.ExecContext(ctx, seed); err != nil {
				t.Fatalf("seed version %d: %v", version, err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}

			store, err := db.Open(ctx, path)
			if err != nil {
				t.Fatalf("open and upgrade: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			savedProvider, err := store.GetProvider(ctx, "saved-provider")
			if err != nil {
				t.Fatalf("get saved provider after upgrade: %v", err)
			}
			if savedProvider.Name != "saved-provider" || savedProvider.AdapterType != "openai" ||
				savedProvider.CreatedAt != 1 || savedProvider.UpdatedAt != 2 {
				t.Fatalf("saved provider identity or seeded fields changed: %+v", savedProvider)
			}
			if savedProvider.DisplayName != "" || savedProvider.BaseUrl != "" || savedProvider.ModelsUrl != "" ||
				savedProvider.DefaultModel != "" || savedProvider.SeedModels != "[]" ||
				savedProvider.Enabled != 1 || savedProvider.Builtin != 0 || savedProvider.ReasoningHistory != "inline" ||
				savedProvider.ContextWindow != 0 || savedProvider.InputCostPerMtok != 0 || savedProvider.OutputCostPerMtok != 0 {
				t.Fatalf("saved provider defaults changed: %+v", savedProvider)
			}
			upgraded := store.DB()
			var contextJSON, usageJSON string
			if err := upgraded.QueryRowContext(ctx, `SELECT context_json, last_usage_json FROM sessions WHERE id = 'legacy'`).Scan(&contextJSON, &usageJSON); err != nil {
				t.Fatal(err)
			}
			if contextJSON != `{"messages":["kept"]}` || usageJSON != `{"input_tokens":17}` {
				t.Fatalf("legacy session changed: context=%q usage=%q", contextJSON, usageJSON)
			}
			var checksum, setting string
			if err := upgraded.QueryRowContext(ctx, `SELECT checksum FROM session_history_batches WHERE session_id = 'legacy' AND batch_id = 'receipt'`).Scan(&checksum); err != nil || checksum != "digest" {
				t.Fatalf("saved history receipt = %q, err=%v", checksum, err)
			}
			if err := upgraded.QueryRowContext(ctx, `SELECT value FROM settings WHERE workspace = '/workspace' AND key = 'provider.endpoint'`).Scan(&setting); err != nil || setting != "https://example.invalid" {
				t.Fatalf("provider setting = %q, err=%v", setting, err)
			}
			var routes int
			if err := upgraded.QueryRowContext(ctx, `SELECT count(*) FROM llm_route_protocols WHERE provider = 'saved-provider'`).Scan(&routes); err != nil {
				t.Fatal(err)
			}
			wantRoutes := 0
			if version == 36 {
				wantRoutes = 1
				protocol, err := store.GetRouteProtocol(ctx, "saved-provider", "chat")
				if err != nil {
					t.Fatalf("get saved route protocol after upgrade: %v", err)
				}
				if protocol != llm.ModelProtocols.PROTOCOLRESPONSES {
					t.Fatalf("saved route protocol = %v, want %v", protocol, llm.ModelProtocols.PROTOCOLRESPONSES)
				}
			}
			if routes != wantRoutes {
				t.Fatalf("route policy count = %d, want %d", routes, wantRoutes)
			}
			accounting, err := store.ListSessionProviderAccounting(ctx, "legacy", 0, 10)
			if err != nil || len(accounting) != 0 {
				t.Fatalf("accounting after upgrade = %v, err=%v; want empty", accounting, err)
			}
			foreignKeys, err := upgraded.QueryContext(ctx, "PRAGMA foreign_key_check")
			if err != nil {
				t.Fatalf("foreign-key check: %v", err)
			}
			defer foreignKeys.Close()
			if foreignKeys.Next() {
				t.Fatal("foreign-key check found a violation")
			}
			if err := foreignKeys.Err(); err != nil {
				t.Fatalf("iterate foreign-key check: %v", err)
			}
		})
	}
}

func migrationVersionName(version int64) string {
	if version == 35 {
		return "from_35"
	}
	return "from_36"
}
