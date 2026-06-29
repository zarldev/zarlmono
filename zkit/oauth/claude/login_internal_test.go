package claude

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestExtractToken_PrefersSkToken(t *testing.T) {
	out := "info: opening browser\nexport CLAUDE_CODE_OAUTH_TOKEN=sk-ant-abc1234567890XYZ_token\n"
	if got := extractToken(out); got != "sk-ant-abc1234567890XYZ_token" {
		t.Fatalf("extractToken() = %q, want sk token", got)
	}
}

func TestExtractToken_ParsesStderrStyleOutput(t *testing.T) {
	out := "Complete sign-in in browser\nCLAUDE_CODE_OAUTH_TOKEN=ccode_ABCdef1234567890token\n"
	if got := extractToken(out); got != "ccode_ABCdef1234567890token" {
		t.Fatalf("extractToken() = %q, want parsed token", got)
	}
}

func TestExtractToken_RejectsAmbiguousOutput(t *testing.T) {
	out := "first_token_12345678901234567890\nsecond_token_12345678901234567890\n"
	if got := extractToken(out); got != "" {
		t.Fatalf("extractToken() = %q, want empty on ambiguity", got)
	}
}

func TestStoreToken_PersistsParsedCredential(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	svc := prefs.NewService(store, nil, "")
	out := "note\nCLAUDE_CODE_OAUTH_TOKEN=ccode_ABCdef1234567890token\n"
	if err := StoreToken(t.Context(), svc, out); err != nil {
		t.Fatalf("StoreToken() error = %v", err)
	}
	got, ok, err := svc.GetKey(t.Context(), prefs.ScopeGlobal, CredProvider)
	if err != nil {
		t.Fatalf("GetKey() error = %v", err)
	}
	if !ok {
		t.Fatal("stored key missing")
	}
	if got == "" {
		t.Fatal("stored credential empty")
	}
}
