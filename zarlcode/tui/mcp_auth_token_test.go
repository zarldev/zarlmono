package tui_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func newVaultSettings(t *testing.T) *engine.Settings {
	t.Helper()
	s, _ := newVaultSettingsWithVault(t)
	return s
}

func newVaultSettingsWithVault(t *testing.T) (*engine.Settings, *vault.Vault) {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	v, err := vault.Open(t.TempDir(), func(_, _ bool) (string, error) { return "test-passphrase", nil })
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	s := engine.NewSettings(store, v, nil, "")
	if _, err := s.Svc.EnableCredentialProtection(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s, v
}

func newMCPFormUI(t *testing.T, settings *engine.Settings) *tui.UI {
	t.Helper()
	ui := tui.New()
	ui.SetSettings(settings)
	model, _ := ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*tui.UI)
}

func seedMCPServer(t *testing.T, s *engine.Settings, row db.MCPServerRow) {
	t.Helper()
	if err := s.Store.UpsertMCPServer(t.Context(), row); err != nil {
		t.Fatalf("seed MCP server: %v", err)
	}
}

func connectionAttempts(t *testing.T, s *engine.Settings) (int32, string) {
	t.Helper()
	var attempts atomic.Int32
	tokens := make(chan string, 1)
	tui.ConnectConfiguredMCPServers(t.Context(), s, func(_ context.Context, _ db.MCPServerRow, token string) error {
		attempts.Add(1)
		tokens <- token
		return nil
	})
	select {
	case token := <-tokens:
		return attempts.Load(), token
	default:
		return attempts.Load(), ""
	}
}

func TestMCPStartupAuth_ValidEncryptedTokenConnects(t *testing.T) {
	s := newVaultSettings(t)
	const name, secret = "github", "ghp_encrypted"
	seedMCPServer(t, s, db.MCPServerRow{Name: name, Transport: "http", BaseURL: "https://mcp.example.com", AuthRequired: true, Enabled: true})
	if err := s.Svc.SetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name), secret); err != nil {
		t.Fatalf("store encrypted token: %v", err)
	}
	if attempts, token := connectionAttempts(t, s); attempts != 1 || token != secret {
		t.Fatalf("connections = %d with token %q; want one with encrypted token", attempts, token)
	}
}

func TestMCPStartupAuth_NoAuthConnectsWithoutBearer(t *testing.T) {
	s := newVaultSettings(t)
	seedMCPServer(t, s, db.MCPServerRow{
		Name: "public", Transport: "http", BaseURL: "https://mcp.example.com", Enabled: true,
	})
	if attempts, token := connectionAttempts(t, s); attempts != 1 || token != "" {
		t.Fatalf("connections = %d with token %q; want one unauthenticated connection", attempts, token)
	}
}

func TestMCPStartupAuth_NoAuthIgnoresAndPreservesStaleEncryptedToken(t *testing.T) {
	unlocked := newVaultSettings(t)
	const name, stale = "public", "stale-token-canary"
	seedMCPServer(t, unlocked, db.MCPServerRow{
		Name: name, Transport: "http", BaseURL: "https://new.example.com", Enabled: true,
	})
	if err := unlocked.Svc.SetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name), stale); err != nil {
		t.Fatalf("seed stale encrypted token: %v", err)
	}
	locked := engine.NewSettings(unlocked.Store, nil, nil, "")
	if attempts, token := connectionAttempts(t, locked); attempts != 1 || token != "" {
		t.Fatalf("connections = %d with token %q; want one unauthenticated connection", attempts, token)
	}
	stored, err := unlocked.Svc.GetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name))
	if err != nil {
		t.Fatalf("read preserved token: %v", err)
	}
	if stored != stale {
		t.Fatalf("preserved token = %q; want stale canary", stored)
	}
}

func TestMCPConfig_BlankTokenActualFormDeletesStaleEncryptedCredential(t *testing.T) {
	s := newVaultSettings(t)
	const name = "repointed"
	if err := s.Svc.SetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name), "old-endpoint-canary"); err != nil {
		t.Fatalf("seed stale encrypted token: %v", err)
	}
	ui := newMCPFormUI(t, s)
	form := tui.NewMCPAddFormHarness(t.Context(), ui, s)
	form.Fill([6]string{name, "http", "", "", "https://new.example.com", ""})
	if cmd := form.Submit(); cmd != nil {
		t.Fatal("blank-token form unexpectedly started credential work")
	}
	if _, err := s.Svc.GetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name)); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("stale credential lookup error = %v; want not found", err)
	}
	rows, err := s.Store.ListMCPServers(t.Context())
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(rows) != 1 || rows[0].BaseURL != "https://new.example.com" || rows[0].AuthRequired {
		t.Fatalf("persisted server = %#v; want new unauthenticated endpoint", rows)
	}
	if form.Adding() {
		t.Fatal("successful blank-token form remained open")
	}
}

func TestMCPConfig_ActualFormCancellationClearsSecretAndPreservesAuthIntent(t *testing.T) {
	ui, settings, _ := newCredentialUI(t)
	const secret = "mcp-cancel-secret-canary"
	form := tui.NewMCPAddFormHarness(t.Context(), ui, settings)
	form.Fill([6]string{"cancelled", "http", "", "", "https://new.example.com", secret})
	if view := form.View(); strings.Contains(view, secret) || !strings.Contains(view, "••••") {
		t.Fatalf("auth composer was not masked: %q", view)
	}
	if cmd := form.Submit(); cmd != nil {
		t.Fatal("opening first-secret vault modal returned async work")
	}
	if _, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil {
		t.Fatal("cancelling vault setup started work")
	}
	if form.Token() != "" || !form.AuthRequired() || !form.Adding() {
		t.Fatalf("cancelled form state: token=%q auth=%v adding=%v", form.Token(), form.AuthRequired(), form.Adding())
	}
	if strings.Contains(form.Status(), secret) || strings.Contains(form.View(), secret) {
		t.Fatal("cancelled form exposed secret canary")
	}
	if cmd := form.Submit(); cmd != nil || !strings.Contains(form.Status(), "auth token required") {
		t.Fatalf("blank retry did not require re-entry: cmd=%v status=%q", cmd != nil, form.Status())
	}
	if rows, err := settings.Store.ListMCPServers(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("servers after cancellation = %#v, %v", rows, err)
	}
}

func TestMCPConfig_ActualFormFailureRollsBackEndpointCredentialAndProtection(t *testing.T) {
	ui, settings, _ := newCredentialUI(t)
	const name = "atomic"
	const oldSecret = "old-endpoint-secret-canary"
	const newSecret = "new-endpoint-secret-canary"
	oldRow := db.MCPServerRow{Name: name, Transport: "http", BaseURL: "https://old.example.com", AuthRequired: true, Enabled: true}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCredentialProtection, prefs.CredentialProtectionOff); err != nil {
		t.Fatal(err)
	}
	if err := settings.Svc.SetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name), oldSecret); err != nil {
		t.Fatal(err)
	}
	seedMCPServer(t, settings, oldRow)
	if err := settings.Svc.DeleteSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCredentialProtection); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Store.DB().ExecContext(t.Context(), `
		CREATE TRIGGER reject_mcp_update BEFORE UPDATE ON mcp_servers
		BEGIN SELECT RAISE(ABORT, 'endpoint write rejected'); END;
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	form := tui.NewMCPAddFormHarness(t.Context(), ui, settings)
	form.Fill([6]string{name, "http", "", "", "https://new.example.com", newSecret})
	if cmd := form.Submit(); cmd != nil {
		t.Fatal("opening first-secret vault modal returned async work")
	}
	typeCredential(t, ui, "atomic passphrase")
	_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeCredential(t, ui, "atomic passphrase")
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("passphrase confirmation did not start persistence")
	}
	_, _ = ui.Update(cmd())

	rows, err := settings.Store.ListMCPServers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BaseURL != oldRow.BaseURL || !rows[0].AuthRequired {
		t.Fatalf("endpoint changed after rollback: %#v", rows)
	}
	stored, err := settings.Store.GetAPIKeyExact(t.Context(), "", tui.MCPAuthKeyProvider(name))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Storage != db.APIKeyStoragePlaintext || string(stored.Ciphertext) != oldSecret {
		t.Fatalf("credential changed after rollback: %#v", stored)
	}
	if _, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyCredentialProtection); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("protection setting committed after rollback: %v", err)
	}
	if form.Token() != "" || !form.AuthRequired() || !form.Adding() {
		t.Fatalf("failed form state: token=%q auth=%v adding=%v", form.Token(), form.AuthRequired(), form.Adding())
	}
	if strings.Contains(form.Status(), newSecret) || strings.Contains(form.View(), newSecret) {
		t.Fatal("failed form exposed new secret canary")
	}
	if cmd := form.Submit(); cmd != nil || !strings.Contains(form.Status(), "auth token required") {
		t.Fatalf("failed blank retry did not require re-entry: cmd=%v status=%q", cmd != nil, form.Status())
	}
}

func TestMCPConfig_ActualFormFirstSecretCommitsProtectedEndpoint(t *testing.T) {
	ui, settings, _ := newCredentialUI(t)
	const name = "protected"
	const secret = "mcp-first-secret-canary"
	form := tui.NewMCPAddFormHarness(t.Context(), ui, settings)
	form.Fill([6]string{name, "http", "", "", "https://protected.example.com", secret})
	if cmd := form.Submit(); cmd != nil {
		t.Fatal("opening first-secret vault modal returned async work")
	}
	typeCredential(t, ui, "protected passphrase")
	_, _ = ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeCredential(t, ui, "protected passphrase")
	_, cmd := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("passphrase confirmation did not start persistence")
	}
	_, _ = ui.Update(cmd())

	rows, err := settings.Store.ListMCPServers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BaseURL != "https://protected.example.com" || !rows[0].AuthRequired || rows[0].AuthToken != "" {
		t.Fatalf("protected endpoint = %#v", rows)
	}
	got, err := settings.Svc.GetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider(name))
	if err != nil || got != secret {
		t.Fatalf("protected token = %q, %v", got, err)
	}
	stored, err := settings.Store.GetAPIKeyExact(t.Context(), "", tui.MCPAuthKeyProvider(name))
	if err != nil || stored.Storage != db.APIKeyStorageVault || string(stored.Ciphertext) == secret {
		t.Fatalf("stored token row = %#v, %v", stored, err)
	}
	if form.Adding() || form.Token() != "" || strings.Contains(form.Status(), secret) || strings.Contains(form.View(), secret) {
		t.Fatalf("successful form retained or exposed secret: adding=%v token=%q status=%q", form.Adding(), form.Token(), form.Status())
	}
}

func TestMCPStartupAuth_ObsoletePlaintextFailsClosedWithoutChangingRow(t *testing.T) {
	s := newVaultSettings(t)
	const obsolete = "obsolete-plaintext-canary"
	seedMCPServer(t, s, db.MCPServerRow{
		Name: "obsolete", Transport: "http", BaseURL: "https://mcp.example.com",
		AuthToken: obsolete, AuthRequired: true, Enabled: true,
	})
	if attempts, _ := connectionAttempts(t, s); attempts != 0 {
		t.Fatalf("connections = %d; want zero", attempts)
	}
	rows, err := s.Store.ListMCPServers(t.Context())
	if err != nil {
		t.Fatalf("list servers after startup: %v", err)
	}
	if rows[0].AuthToken != obsolete {
		t.Fatalf("obsolete plaintext changed: %q", rows[0].AuthToken)
	}
}

func TestHeadlessMCPSetup_CompletesBeforeRunAndFailsClosedPerServer(t *testing.T) {
	s := newVaultSettings(t)
	seedMCPServer(t, s, db.MCPServerRow{
		Name: "required", Transport: "http", BaseURL: "https://required.example.com", AuthRequired: true, Enabled: true,
	})
	seedMCPServer(t, s, db.MCPServerRow{
		Name: "healthy", Transport: "http", BaseURL: "https://healthy.example.com", Enabled: true,
	})

	var requiredDials atomic.Int32
	var healthyAvailable atomic.Bool
	const wantExit = 37
	exit := tui.RunHeadlessWithConfiguredMCP(t.Context(), s, func(_ context.Context, row db.MCPServerRow, token string) error {
		switch row.Name {
		case "required":
			requiredDials.Add(1)
		case "healthy":
			if token != "" {
				return errors.New("healthy server received authentication")
			}
			healthyAvailable.Store(true)
		}
		return nil
	}, func() int {
		if !healthyAvailable.Load() {
			t.Error("headless process started before healthy MCP became available")
		}
		return wantExit
	})
	if exit != wantExit {
		t.Fatalf("exit = %d; want %d", exit, wantExit)
	}
	if got := requiredDials.Load(); got != 0 {
		t.Fatalf("required-auth dials = %d; want zero", got)
	}
	if !healthyAvailable.Load() {
		t.Fatal("healthy no-auth MCP was not connected")
	}
}

func TestMCPStartupAuth_UnavailableCredentialsDoNotConnect(t *testing.T) {
	t.Run("missing credential service", func(t *testing.T) {
		store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		s := &engine.Settings{Store: store}
		seedMCPServer(t, s, db.MCPServerRow{Name: "missing", Transport: "http", BaseURL: "https://mcp.example.com", AuthRequired: true, Enabled: true})
		if attempts, _ := connectionAttempts(t, s); attempts != 0 {
			t.Fatalf("connections = %d; want zero", attempts)
		}
	})
	t.Run("required token absent", func(t *testing.T) {
		s := newVaultSettings(t)
		seedMCPServer(t, s, db.MCPServerRow{Name: "required", Transport: "http", BaseURL: "https://mcp.example.com", AuthRequired: true, Enabled: true})
		if attempts, _ := connectionAttempts(t, s); attempts != 0 {
			t.Fatalf("connections = %d; want zero", attempts)
		}
	})
	t.Run("missing encrypted token", func(t *testing.T) {
		s, v := newVaultSettingsWithVault(t)
		seedMCPServer(t, s, db.MCPServerRow{Name: "empty", Transport: "http", BaseURL: "https://mcp.example.com", AuthRequired: true, Enabled: true})
		ciphertext, nonce, err := v.Encrypt("")
		if err != nil {
			t.Fatalf("encrypt empty token: %v", err)
		}
		if err := s.Store.SetAPIKey(t.Context(), "", tui.MCPAuthKeyProvider("empty"), db.APIKeyCiphertext{
			Ciphertext: ciphertext, Nonce: nonce, KeyVersion: vault.CurrentKeyVersion, Storage: db.APIKeyStorageVault,
		}); err != nil {
			t.Fatalf("seed empty encrypted token: %v", err)
		}
		if attempts, _ := connectionAttempts(t, s); attempts != 0 {
			t.Fatalf("connections = %d; want zero", attempts)
		}
	})

	t.Run("locked", func(t *testing.T) {
		unlocked := newVaultSettings(t)
		seedMCPServer(t, unlocked, db.MCPServerRow{Name: "locked", Transport: "http", BaseURL: "https://mcp.example.com", AuthRequired: true, Enabled: true})
		if err := unlocked.Svc.SetKey(t.Context(), prefs.ScopeGlobal, tui.MCPAuthKeyProvider("locked"), "locked-canary"); err != nil {
			t.Fatalf("store encrypted token: %v", err)
		}
		locked := engine.NewSettings(unlocked.Store, nil, nil, "")
		if _, _, err := tui.ResolveMCPAuthToken(t.Context(), locked, "locked"); !errors.Is(err, prefs.ErrCredentialsLocked) {
			t.Fatalf("resolve error = %v; want credentials locked", err)
		}
		if attempts, _ := connectionAttempts(t, locked); attempts != 0 {
			t.Fatalf("connections = %d; want zero", attempts)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		s := newVaultSettings(t)
		seedMCPServer(t, s, db.MCPServerRow{Name: "future", Transport: "http", BaseURL: "https://mcp.example.com", AuthRequired: true, Enabled: true})
		before := db.APIKeyCiphertext{Ciphertext: []byte("future-canary"), Nonce: []byte("nonce"), KeyVersion: vault.CurrentKeyVersion + 1, Storage: db.APIKeyStorageVault}
		if err := s.Store.SetAPIKey(t.Context(), "", tui.MCPAuthKeyProvider("future"), before); err != nil {
			t.Fatalf("seed unsupported token: %v", err)
		}
		if _, _, err := tui.ResolveMCPAuthToken(t.Context(), s, "future"); !errors.Is(err, prefs.ErrUnsupportedCredentialFormat) {
			t.Fatalf("resolve error = %v; want unsupported format", err)
		}
		if attempts, _ := connectionAttempts(t, s); attempts != 0 {
			t.Fatalf("connections = %d; want zero", attempts)
		}
		after, err := s.Store.GetAPIKeyExact(t.Context(), "", tui.MCPAuthKeyProvider("future"))
		if err != nil {
			t.Fatalf("read token after startup: %v", err)
		}
		if string(after.Ciphertext) != string(before.Ciphertext) || string(after.Nonce) != string(before.Nonce) || after.KeyVersion != before.KeyVersion {
			t.Fatalf("unsupported credential changed: %#v", after)
		}
	})

	t.Run("storage error", func(t *testing.T) {
		s := newVaultSettings(t)
		seedMCPServer(t, s, db.MCPServerRow{Name: "storage", Transport: "http", BaseURL: "https://mcp.example.com", Enabled: true})
		if err := s.Store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		if attempts, _ := connectionAttempts(t, s); attempts != 0 {
			t.Fatalf("connections = %d; want zero", attempts)
		}
	})
}
