package prefs_test

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// openTestStore returns a Store backed by a fresh sqlite file in
// t.TempDir(). Migrations run as part of Open, so the returned Store is
// schema-current.
func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// openTestVault creates a Vault with a deterministic key via
// $ZARLCODE_KEY so encrypt/decrypt is reproducible across calls in
// the same test.
func openTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	// 32-byte zero key, base64-encoded — deterministic and valid.
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}

// openTestService is the common setup: fresh store + vault + a
// non-empty wsRoot.
func openTestService(t *testing.T) *prefs.Service {
	t.Helper()
	store := openTestStore(t)
	v := openTestVault(t)
	return prefs.NewService(store, v, "/home/test/project")
}

// openTestServiceNoVault returns a Service without a vault — used to
// exercise ErrNoVault paths.
func openTestServiceNoVault(t *testing.T) *prefs.Service {
	t.Helper()
	store := openTestStore(t)
	return prefs.NewService(store, nil, "/home/test/project")
}

// openTestServiceNoWorkspace returns a Service with wsRoot="" — used
// to exercise ErrNoWorkspace paths.
func openTestServiceNoWorkspace(t *testing.T) *prefs.Service {
	t.Helper()
	store := openTestStore(t)
	v := openTestVault(t)
	return prefs.NewService(store, v, "")
}

func TestService_SetSetting_ScopeWorkspace_Roundtrip(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	err := svc.SetSetting(ctx, prefs.ScopeWorkspace, "editor", "vim")
	if err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}

	sv, ok, err := svc.GetSetting(ctx, prefs.ScopeWorkspace, "editor")
	if err != nil {
		t.Fatalf("GetSetting workspace: %v", err)
	}
	if !ok {
		t.Fatal("GetSetting workspace: not found")
	}
	if sv.Value != "vim" {
		t.Errorf("value = %q, want vim", sv.Value)
	}
	if sv.Source != prefs.ScopeWorkspace {
		t.Errorf("source = %v, want workspace", sv.Source)
	}
}

func TestService_SetSetting_ScopeWorkspace_ErrNoWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	err := svc.SetSetting(ctx, prefs.ScopeWorkspace, "editor", "vim")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("SetSetting: err = %v, want ErrNoWorkspace", err)
	}

	_, _, err = svc.GetSetting(ctx, prefs.ScopeWorkspace, "editor")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("GetSetting: err = %v, want ErrNoWorkspace", err)
	}

	err = svc.DeleteSetting(ctx, prefs.ScopeWorkspace, "editor")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("DeleteSetting: err = %v, want ErrNoWorkspace", err)
	}

	err = svc.PromoteSetting(ctx, "editor")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("PromoteSetting: err = %v, want ErrNoWorkspace", err)
	}
}

func TestService_SetSetting_ScopeGlobal_Roundtrip(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	// Service with empty wsRoot — global scope must still work.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	err := svc.SetSetting(ctx, prefs.ScopeGlobal, "theme", "dark")
	if err != nil {
		t.Fatalf("SetSetting global: %v", err)
	}

	sv, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, "theme")
	if err != nil {
		t.Fatalf("GetSetting global: %v", err)
	}
	if !ok {
		t.Fatal("GetSetting global: not found")
	}
	if sv.Value != "dark" {
		t.Errorf("value = %q, want dark", sv.Value)
	}
	if sv.Source != prefs.ScopeGlobal {
		t.Errorf("source = %v, want global", sv.Source)
	}
}

func TestService_GetSetting_ScopeEffective_Fallback(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// Neither set → not found.
	_, ok, err := svc.GetSetting(ctx, prefs.ScopeEffective, "model")
	if err != nil {
		t.Fatalf("GetSetting effective (empty): %v", err)
	}
	if ok {
		t.Fatal("expected not found when nothing set")
	}

	// Set global only → effective returns global value.
	err = svc.SetSetting(ctx, prefs.ScopeGlobal, "model", "global-model")
	if err != nil {
		t.Fatalf("SetSetting global: %v", err)
	}
	sv, ok, err := svc.GetSetting(ctx, prefs.ScopeEffective, "model")
	if err != nil {
		t.Fatalf("GetSetting effective (global only): %v", err)
	}
	if !ok {
		t.Fatal("expected found after global set")
	}
	if sv.Value != "global-model" {
		t.Errorf("value = %q, want global-model", sv.Value)
	}
	if sv.Source != prefs.ScopeGlobal {
		t.Errorf("source = %v, want global", sv.Source)
	}

	// Set workspace → effective returns workspace value (wins over global).
	err = svc.SetSetting(ctx, prefs.ScopeWorkspace, "model", "ws-model")
	if err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}
	sv, ok, err = svc.GetSetting(ctx, prefs.ScopeEffective, "model")
	if err != nil {
		t.Fatalf("GetSetting effective (both): %v", err)
	}
	if !ok {
		t.Fatal("expected found after workspace set")
	}
	if sv.Value != "ws-model" {
		t.Errorf("value = %q, want ws-model", sv.Value)
	}
	if sv.Source != prefs.ScopeWorkspace {
		t.Errorf("source = %v, want workspace", sv.Source)
	}
}

func TestService_SetSetting_EmptyValueRejection(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	err := svc.SetSetting(ctx, prefs.ScopeGlobal, "theme", "")
	if err == nil {
		t.Fatal("expected error for empty value")
	}
	if !strings.Contains(err.Error(), "empty value") {
		t.Errorf("error = %v, want mention of empty value", err)
	}
}

func TestService_WriteRejection_ScopeEffective(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	err := svc.SetSetting(ctx, prefs.ScopeEffective, "theme", "dark")
	if !errors.Is(err, prefs.ErrInvalidScope) {
		t.Errorf("SetSetting ScopeEffective: err = %v, want ErrInvalidScope", err)
	}

	err = svc.DeleteSetting(ctx, prefs.ScopeEffective, "theme")
	if !errors.Is(err, prefs.ErrInvalidScope) {
		t.Errorf("DeleteSetting ScopeEffective: err = %v, want ErrInvalidScope", err)
	}

	err = svc.SetKey(ctx, prefs.ScopeEffective, "openai", "sk-test")
	if !errors.Is(err, prefs.ErrInvalidScope) {
		t.Errorf("SetKey ScopeEffective: err = %v, want ErrInvalidScope", err)
	}

	err = svc.DeleteKey(ctx, prefs.ScopeEffective, "openai")
	if !errors.Is(err, prefs.ErrInvalidScope) {
		t.Errorf("DeleteKey ScopeEffective: err = %v, want ErrInvalidScope", err)
	}
}

func TestService_PromoteSetting_Move(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// No workspace row → error.
	err := svc.PromoteSetting(ctx, "theme")
	if err == nil {
		t.Fatal("expected error promoting non-existent setting")
	}

	// Set workspace value.
	err = svc.SetSetting(ctx, prefs.ScopeWorkspace, "theme", "light")
	if err != nil {
		t.Fatalf("SetSetting workspace: %v", err)
	}

	// Promote.
	err = svc.PromoteSetting(ctx, "theme")
	if err != nil {
		t.Fatalf("PromoteSetting: %v", err)
	}

	// Workspace row should be gone.
	_, ok, err := svc.GetSetting(ctx, prefs.ScopeWorkspace, "theme")
	if err != nil {
		t.Fatalf("GetSetting workspace after promote: %v", err)
	}
	if ok {
		t.Fatal("workspace row still exists after promote")
	}

	// Global row should have the value.
	sv, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, "theme")
	if err != nil {
		t.Fatalf("GetSetting global after promote: %v", err)
	}
	if !ok {
		t.Fatal("global row missing after promote")
	}
	if sv.Value != "light" {
		t.Errorf("global value = %q, want light", sv.Value)
	}
}

func TestService_SetKey_Roundtrip(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	err := svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "sk-test-plaintext")
	if err != nil {
		t.Fatalf("SetKey workspace: %v", err)
	}

	// Read back via GetKeyEffective.
	kv, ok, err := svc.GetKeyEffective(ctx, "openai")
	if err != nil {
		t.Fatalf("GetKeyEffective: %v", err)
	}
	if !ok {
		t.Fatal("GetKeyEffective: not found")
	}
	if kv.Value != "sk-test-plaintext" {
		t.Errorf("value = %q, want sk-test-plaintext", kv.Value)
	}
	if kv.Source != prefs.ScopeWorkspace {
		t.Errorf("source = %v, want workspace", kv.Source)
	}

	// Read back via GetKey with explicit scope.
	val, ok, err := svc.GetKey(ctx, prefs.ScopeWorkspace, "openai")
	if err != nil {
		t.Fatalf("GetKey workspace: %v", err)
	}
	if !ok {
		t.Fatal("GetKey workspace: not found")
	}
	if val != "sk-test-plaintext" {
		t.Errorf("value = %q, want sk-test-plaintext", val)
	}
}

func TestService_SetKey_EmptyRejection(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	err := svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "")
	if err == nil {
		t.Fatal("expected error for empty plaintext")
	}
	if !strings.Contains(err.Error(), "empty plaintext") {
		t.Errorf("error = %v, want mention of empty plaintext", err)
	}
}

func TestService_NoVaultPlaintextMode(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv in other tests.
	svc := openTestServiceNoVault(t)
	ctx := t.Context()

	if svc.HasVault() {
		t.Fatal("HasVault returned true for nil vault")
	}

	if err := svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "sk-test"); err != nil {
		t.Fatalf("SetKey plaintext without vault: %v", err)
	}

	got, ok, err := svc.GetKey(ctx, prefs.ScopeGlobal, "openai")
	if err != nil {
		t.Fatalf("GetKey plaintext without vault: %v", err)
	}
	if !ok || got != "sk-test" {
		t.Fatalf("GetKey plaintext = %q, %v; want sk-test, true", got, ok)
	}

	kv, ok, err := svc.GetKeyEffective(ctx, "openai")
	if err != nil {
		t.Fatalf("GetKeyEffective plaintext without vault: %v", err)
	}
	if !ok || kv.Value != "sk-test" || kv.Source != prefs.ScopeGlobal {
		t.Fatalf("GetKeyEffective = %#v, %v; want global sk-test", kv, ok)
	}

	if err := svc.DeleteKey(ctx, prefs.ScopeGlobal, "openai"); err != nil {
		t.Errorf("DeleteKey without vault: %v", err)
	}
}

func TestService_ListKeys_Union(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// Set keys at workspace scope.
	err := svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "sk-openai")
	if err != nil {
		t.Fatalf("SetKey openai workspace: %v", err)
	}
	err = svc.SetKey(ctx, prefs.ScopeWorkspace, "anthropic", "sk-anthropic")
	if err != nil {
		t.Fatalf("SetKey anthropic workspace: %v", err)
	}

	// Set key at global scope (different provider, shared provider).
	err = svc.SetKey(ctx, prefs.ScopeGlobal, "google", "sk-google")
	if err != nil {
		t.Fatalf("SetKey google global: %v", err)
	}
	// Also set anthropic globally — the union should deduplicate.
	err = svc.SetKey(ctx, prefs.ScopeGlobal, "anthropic", "sk-anthropic-global")
	if err != nil {
		t.Fatalf("SetKey anthropic global: %v", err)
	}

	// ScopeEffective union (order not guaranteed — the Service doesn't sort).
	providers, err := svc.ListKeys(ctx, prefs.ScopeEffective)
	if err != nil {
		t.Fatalf("ListKeys effective: %v", err)
	}

	if len(providers) != 3 {
		t.Fatalf("ListKeys effective: got %d providers %v, want 3", len(providers), providers)
	}
	for _, want := range []string{"anthropic", "google", "openai"} {
		found := false
		for _, p := range providers {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("provider %q missing from effective list %v", want, providers)
		}
	}

	// ScopeWorkspace: the Store's ListAPIKeyProviders unions workspace + global,
	// so workspace scope also sees global providers.
	wsProviders, err := svc.ListKeys(ctx, prefs.ScopeWorkspace)
	if err != nil {
		t.Fatalf("ListKeys workspace: %v", err)
	}
	// All three providers visible (workspace + global union).
	if len(wsProviders) != 3 {
		t.Errorf("workspace providers = %v, want 3 providers (union of ws+global)", wsProviders)
	}

	// ScopeGlobal only.
	globalProviders, err := svc.ListKeys(ctx, prefs.ScopeGlobal)
	if err != nil {
		t.Fatalf("ListKeys global: %v", err)
	}
	globalExpected := []string{"anthropic", "google"}
	if strings.Join(globalProviders, ",") != strings.Join(globalExpected, ",") {
		t.Errorf("global providers = %v, want %v", globalProviders, globalExpected)
	}
}

func TestService_Delete_Idempotent(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// Delete non-existent setting — no error.
	err := svc.DeleteSetting(ctx, prefs.ScopeWorkspace, "nonexistent")
	if err != nil {
		t.Errorf("DeleteSetting non-existent: %v", err)
	}
	err = svc.DeleteSetting(ctx, prefs.ScopeGlobal, "nonexistent")
	if err != nil {
		t.Errorf("DeleteSetting non-existent global: %v", err)
	}

	// Delete non-existent key — no error.
	err = svc.DeleteKey(ctx, prefs.ScopeWorkspace, "nonexistent")
	if err != nil {
		t.Errorf("DeleteKey non-existent: %v", err)
	}
	err = svc.DeleteKey(ctx, prefs.ScopeGlobal, "nonexistent")
	if err != nil {
		t.Errorf("DeleteKey non-existent global: %v", err)
	}
}

func TestService_UnknownScope(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	unknown := prefs.Scope(99)

	_, _, err := svc.GetSetting(ctx, unknown, "theme")
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("GetSetting: err = %v, want unknown scope", err)
	}

	err = svc.SetSetting(ctx, unknown, "theme", "value")
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("SetSetting: err = %v, want unknown scope", err)
	}

	err = svc.DeleteSetting(ctx, unknown, "theme")
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("DeleteSetting: err = %v, want unknown scope", err)
	}

	// Key methods require a vault.
	svcVault := openTestService(t)
	_, _, err = svcVault.GetKey(ctx, unknown, "openai")
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("GetKey: err = %v, want unknown scope", err)
	}

	err = svcVault.SetKey(ctx, unknown, "openai", "sk-test")
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("SetKey: err = %v, want unknown scope", err)
	}

	err = svcVault.DeleteKey(ctx, unknown, "openai")
	if err == nil || !strings.Contains(err.Error(), "unknown scope") {
		t.Errorf("DeleteKey: err = %v, want unknown scope", err)
	}
}

func TestService_GetKey_Effective_PrefersWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// Set global key.
	err := svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "sk-global")
	if err != nil {
		t.Fatalf("SetKey global: %v", err)
	}

	kv, ok, err := svc.GetKeyEffective(ctx, "openai")
	if err != nil {
		t.Fatalf("GetKeyEffective (global only): %v", err)
	}
	if !ok {
		t.Fatal("expected found")
	}
	if kv.Value != "sk-global" {
		t.Errorf("value = %q, want sk-global", kv.Value)
	}
	if kv.Source != prefs.ScopeGlobal {
		t.Errorf("source = %v, want global", kv.Source)
	}

	// Set workspace key — should now win.
	err = svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "sk-ws")
	if err != nil {
		t.Fatalf("SetKey workspace: %v", err)
	}

	kv, ok, err = svc.GetKeyEffective(ctx, "openai")
	if err != nil {
		t.Fatalf("GetKeyEffective (both): %v", err)
	}
	if !ok {
		t.Fatal("expected found")
	}
	if kv.Value != "sk-ws" {
		t.Errorf("value = %q, want sk-ws", kv.Value)
	}
	if kv.Source != prefs.ScopeWorkspace {
		t.Errorf("source = %v, want workspace", kv.Source)
	}
}

func TestService_PromoteKey_Move(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// No workspace row → error.
	err := svc.PromoteKey(ctx, "openai")
	if err == nil {
		t.Fatal("expected error promoting non-existent key")
	}

	// Set workspace key.
	err = svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "sk-promote-me")
	if err != nil {
		t.Fatalf("SetKey workspace: %v", err)
	}

	// Promote.
	err = svc.PromoteKey(ctx, "openai")
	if err != nil {
		t.Fatalf("PromoteKey: %v", err)
	}

	// Workspace row should be gone.
	_, ok, err := svc.GetKey(ctx, prefs.ScopeWorkspace, "openai")
	if err != nil {
		t.Fatalf("GetKey workspace after promote: %v", err)
	}
	if ok {
		t.Fatal("workspace key row still exists after promote")
	}

	// Global row should have the plaintext.
	kv, ok, err := svc.GetKeyEffective(ctx, "openai")
	if err != nil {
		t.Fatalf("GetKeyEffective after promote: %v", err)
	}
	if !ok {
		t.Fatal("global key row missing after promote")
	}
	if kv.Value != "sk-promote-me" {
		t.Errorf("global value = %q, want sk-promote-me", kv.Value)
	}
	if kv.Source != prefs.ScopeGlobal {
		t.Errorf("source = %v, want global", kv.Source)
	}
}

func TestService_DeleteKey_IdempotentWithVault(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// Delete a key that doesn't exist should not error.
	err := svc.DeleteKey(ctx, prefs.ScopeWorkspace, "nonexistent")
	if err != nil {
		t.Errorf("DeleteKey non-existent: %v", err)
	}

	// Set then delete.
	err = svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "sk-to-delete")
	if err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	err = svc.DeleteKey(ctx, prefs.ScopeWorkspace, "openai")
	if err != nil {
		t.Errorf("DeleteKey existing: %v", err)
	}

	// Double-delete should be fine.
	err = svc.DeleteKey(ctx, prefs.ScopeWorkspace, "openai")
	if err != nil {
		t.Errorf("DeleteKey double: %v", err)
	}
}

func TestService_HasVault(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.

	withVault := openTestService(t)
	if !withVault.HasVault() {
		t.Error("HasVault false when vault is set")
	}

	withoutVault := openTestServiceNoVault(t)
	if withoutVault.HasVault() {
		t.Error("HasVault true when vault is nil")
	}
}

func TestService_Scope_String(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.

	tests := []struct {
		scope prefs.Scope
		want  string
	}{
		{prefs.ScopeWorkspace, "workspace"},
		{prefs.ScopeGlobal, "global"},
		{prefs.ScopeEffective, "effective"},
		{prefs.Scope(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.scope.String(); got != tt.want {
			t.Errorf("Scope(%d).String() = %q, want %q", tt.scope, got, tt.want)
		}
	}
}

// TestService_GetSetting_ExactScopes ensures that GetSetting with
// ScopeWorkspace only returns workspace values and does not fall
// back to global.
func TestService_GetSetting_ExactScopes(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestService(t)
	ctx := t.Context()

	// Set only global — workspace should not find it.
	err := svc.SetSetting(ctx, prefs.ScopeGlobal, "editor", "nano")
	if err != nil {
		t.Fatalf("SetSetting global: %v", err)
	}

	_, ok, err := svc.GetSetting(ctx, prefs.ScopeWorkspace, "editor")
	if err != nil {
		t.Fatalf("GetSetting workspace: %v", err)
	}
	if ok {
		t.Fatal("workspace should not fall back to global")
	}

	// Global should find it.
	sv, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, "editor")
	if err != nil {
		t.Fatalf("GetSetting global: %v", err)
	}
	if !ok {
		t.Fatal("global not found")
	}
	if sv.Value != "nano" {
		t.Errorf("value = %q, want nano", sv.Value)
	}
}

// TestService_KeyMethods_ErrNoWorkspace ensures key methods return
// ErrNoWorkspace when wsRoot is empty.
func TestService_KeyMethods_ErrNoWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	_, _, err := svc.GetKey(ctx, prefs.ScopeWorkspace, "openai")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("GetKey workspace: err = %v, want ErrNoWorkspace", err)
	}

	err = svc.SetKey(ctx, prefs.ScopeWorkspace, "openai", "sk-test")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("SetKey workspace: err = %v, want ErrNoWorkspace", err)
	}

	err = svc.DeleteKey(ctx, prefs.ScopeWorkspace, "openai")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("DeleteKey workspace: err = %v, want ErrNoWorkspace", err)
	}

	err = svc.PromoteKey(ctx, "openai")
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("PromoteKey: err = %v, want ErrNoWorkspace", err)
	}
}

// TestService_GetKeyEffective_WithoutWorkspace uses wsRoot="" and
// verifies that only the global key is returned.
func TestService_GetKeyEffective_WithoutWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	err := svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "sk-global-only")
	if err != nil {
		t.Fatalf("SetKey global: %v", err)
	}

	kv, ok, err := svc.GetKeyEffective(ctx, "openai")
	if err != nil {
		t.Fatalf("GetKeyEffective: %v", err)
	}
	if !ok {
		t.Fatal("expected found")
	}
	if kv.Value != "sk-global-only" {
		t.Errorf("value = %q, want sk-global-only", kv.Value)
	}
	if kv.Source != prefs.ScopeGlobal {
		t.Errorf("source = %v, want global", kv.Source)
	}
}

// TestService_ListKeys_ErrNoWorkspace verifies ListKeys returns
// ErrNoWorkspace for workspace scope when wsRoot is empty.
func TestService_ListKeys_ErrNoWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	_, err := svc.ListKeys(ctx, prefs.ScopeWorkspace)
	if !errors.Is(err, prefs.ErrNoWorkspace) {
		t.Errorf("ListKeys workspace: err = %v, want ErrNoWorkspace", err)
	}
}

// TestService_ListKeys_WithoutWorkspace verifies that ListKeys with
// ScopeEffective still works when wsRoot is empty (only global).
func TestService_ListKeys_WithoutWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	err := svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "sk-test")
	if err != nil {
		t.Fatalf("SetKey global: %v", err)
	}

	providers, err := svc.ListKeys(ctx, prefs.ScopeEffective)
	if err != nil {
		t.Fatalf("ListKeys effective: %v", err)
	}
	if len(providers) != 1 || providers[0] != "openai" {
		t.Errorf("providers = %v, want [openai]", providers)
	}

	globalProviders, err := svc.ListKeys(ctx, prefs.ScopeGlobal)
	if err != nil {
		t.Fatalf("ListKeys global: %v", err)
	}
	if len(globalProviders) != 1 || globalProviders[0] != "openai" {
		t.Errorf("global providers = %v, want [openai]", globalProviders)
	}
}

// TestService_GetSetting_Effective_WithoutWorkspace verifies that
// ScopeEffective falls back to global when wsRoot is empty (the
// workspace check is simply skipped).
func TestService_GetSetting_Effective_WithoutWorkspace(t *testing.T) {
	// Not parallel — openTestVault uses t.Setenv.
	svc := openTestServiceNoWorkspace(t)
	ctx := t.Context()

	err := svc.SetSetting(ctx, prefs.ScopeGlobal, "theme", "global-theme")
	if err != nil {
		t.Fatalf("SetSetting global: %v", err)
	}

	sv, ok, err := svc.GetSetting(ctx, prefs.ScopeEffective, "theme")
	if err != nil {
		t.Fatalf("GetSetting effective: %v", err)
	}
	if !ok {
		t.Fatal("expected found")
	}
	if sv.Value != "global-theme" {
		t.Errorf("value = %q, want global-theme", sv.Value)
	}
	if sv.Source != prefs.ScopeGlobal {
		t.Errorf("source = %v, want global", sv.Source)
	}
}

// TestService_MigrateVaultKeys exercises the legacy→passphrase migration end
// to end: a credential encrypted under the old random master.key must come out
// readable under the new passphrase-derived key, and master.key must be gone.
func TestService_MigrateVaultKeys(t *testing.T) {
	// db.DefaultDir resolves from $HOME; point it at a throwaway dir so the
	// test never touches the real ~/.zarlcode.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ZARLCODE_PASSPHRASE", "")
	home, err := db.DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	store := openTestStore(t)

	// A random legacy key (what the pre-passphrase binary generated).
	kOld := make([]byte, 32)
	if _, err := rand.Read(kOld); err != nil {
		t.Fatal(err)
	}

	// 1. Seed a credential encrypted under kOld (via the raw-key path, no
	//    master.key on disk yet so the vault has no legacy).
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(kOld))
	vOld, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open old vault: %v", err)
	}
	svcOld := prefs.NewService(store, vOld, "")
	if err := svcOld.SetKey(t.Context(), prefs.ScopeGlobal, "openai", "sk-OLD"); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	// 2. Simulate the upgrade: drop kOld as the legacy master.key file and
	//    switch to a passphrase-derived primary.
	if err := os.WriteFile(filepath.Join(home, "master.key"), kOld, 0o600); err != nil {
		t.Fatalf("write legacy key: %v", err)
	}
	t.Setenv("ZARLCODE_KEY", "")
	vNew, err := vault.Open(func(_, _ bool) (string, error) { return "pp", nil })
	if err != nil {
		t.Fatalf("open passphrase vault: %v", err)
	}
	if !vNew.HasLegacy() {
		t.Fatal("new vault should see the legacy master.key")
	}
	svcNew := prefs.NewService(store, vNew, "")

	// 3. Migrate.
	n, err := svcNew.MigrateVaultKeys(t.Context())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 1 {
		t.Errorf("migrated %d keys, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(home, "master.key")); !os.IsNotExist(err) {
		t.Errorf("master.key should be removed after migration, stat err = %v", err)
	}

	// 4. The credential reads back under the new key — and crucially, still
	//    reads back when reopened with ONLY the passphrase (no legacy present).
	got, ok, err := svcNew.GetKey(t.Context(), prefs.ScopeGlobal, "openai")
	if err != nil || !ok || got != "sk-OLD" {
		t.Fatalf("GetKey after migrate = %q,%v,%v; want sk-OLD,true,nil", got, ok, err)
	}
	vFinal, err := vault.Open(func(_, _ bool) (string, error) { return "pp", nil })
	if err != nil {
		t.Fatalf("reopen passphrase-only: %v", err)
	}
	if vFinal.HasLegacy() {
		t.Error("legacy should be gone after migration")
	}
	got, ok, err = prefs.NewService(store, vFinal, "").GetKey(t.Context(), prefs.ScopeGlobal, "openai")
	if err != nil || !ok || got != "sk-OLD" {
		t.Fatalf("GetKey passphrase-only = %q,%v,%v; want sk-OLD,true,nil", got, ok, err)
	}
}

func TestService_PlaintextStorageDefaultWithoutVault(t *testing.T) {
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "/home/test/project")
	ctx := t.Context()

	mode, err := svc.CredentialProtection(ctx)
	if err != nil {
		t.Fatalf("CredentialProtection: %v", err)
	}
	if mode != prefs.CredentialProtectionOff {
		t.Fatalf("CredentialProtection = %q, want off", mode)
	}

	if err := svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "sk-plain"); err != nil {
		t.Fatalf("SetKey plaintext: %v", err)
	}
	row, ok, err := store.GetAPIKeyExact(ctx, "", "openai")
	if err != nil {
		t.Fatalf("GetAPIKeyExact: %v", err)
	}
	if !ok {
		t.Fatal("stored key row missing")
	}
	if row.Storage != db.APIKeyStoragePlaintext {
		t.Fatalf("storage = %q, want plaintext", row.Storage)
	}
	if got := string(row.Ciphertext); got != "sk-plain" {
		t.Fatalf("stored plaintext = %q, want sk-plain", got)
	}
}

func TestService_CredentialProtectionEnableDisableMigratesRows(t *testing.T) {
	store := openTestStore(t)
	v := openTestVault(t)
	svc := prefs.NewService(store, v, "/home/test/project")
	ctx := t.Context()

	if err := svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyCredentialProtection, prefs.CredentialProtectionOff); err != nil {
		t.Fatalf("set protection off: %v", err)
	}
	if err := svc.SetKey(ctx, prefs.ScopeGlobal, "openai", "sk-migrate"); err != nil {
		t.Fatalf("seed plaintext key: %v", err)
	}

	n, err := svc.EnableCredentialProtection(ctx, nil)
	if err != nil {
		t.Fatalf("EnableCredentialProtection: %v", err)
	}
	if n != 1 {
		t.Fatalf("enabled migrated %d rows, want 1", n)
	}
	row, ok, err := store.GetAPIKeyExact(ctx, "", "openai")
	if err != nil || !ok {
		t.Fatalf("read encrypted row: ok=%v err=%v", ok, err)
	}
	if row.Storage != db.APIKeyStorageVault {
		t.Fatalf("enabled storage = %q, want vault", row.Storage)
	}
	if string(row.Ciphertext) == "sk-migrate" {
		t.Fatal("encrypted row still stores plaintext bytes")
	}
	locked := prefs.NewService(store, nil, "/home/test/project")
	if _, _, err := locked.GetKey(ctx, prefs.ScopeGlobal, "openai"); !errors.Is(err, prefs.ErrCredentialsLocked) {
		t.Fatalf("locked GetKey err = %v, want ErrCredentialsLocked", err)
	}

	n, err = svc.DisableCredentialProtection(ctx, nil)
	if err != nil {
		t.Fatalf("DisableCredentialProtection: %v", err)
	}
	if n != 1 {
		t.Fatalf("disabled migrated %d rows, want 1", n)
	}
	plain := prefs.NewService(store, nil, "/home/test/project")
	got, ok, err := plain.GetKey(ctx, prefs.ScopeGlobal, "openai")
	if err != nil {
		t.Fatalf("plaintext GetKey after disable: %v", err)
	}
	if !ok || got != "sk-migrate" {
		t.Fatalf("plaintext GetKey = %q, %v; want sk-migrate, true", got, ok)
	}
}
