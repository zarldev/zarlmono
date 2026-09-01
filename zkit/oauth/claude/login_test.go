package claude_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth/claude"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestStoreTokenExtractsAndPersistsCredential(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "prefers sk token",
			output: "info: opening browser\nexport CLAUDE_CODE_OAUTH_TOKEN=sk-ant-abc1234567890XYZ_token\n",
			want:   "sk-ant-abc1234567890XYZ_token",
		},
		{
			name:   "parses stderr style output",
			output: "Complete sign-in in browser\nCLAUDE_CODE_OAUTH_TOKEN=ccode_ABCdef1234567890token\n",
			want:   "ccode_ABCdef1234567890token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := openTestService(t)
			if err := claude.StoreToken(t.Context(), svc, tt.output); err != nil {
				t.Fatalf("StoreToken() error = %v", err)
			}

			tok, err := claude.NewTokenSource(svc).Token(t.Context())
			if err != nil {
				t.Fatalf("Token() error = %v", err)
			}
			if tok.Access != tt.want {
				t.Errorf("Token().Access = %q, want %q", tok.Access, tt.want)
			}
		})
	}
}

func TestStoreTokenRejectsAmbiguousOutput(t *testing.T) {
	svc := openTestService(t)
	output := "first_token_12345678901234567890\nsecond_token_12345678901234567890\n"
	if err := claude.StoreToken(t.Context(), svc, output); err == nil {
		t.Fatal("StoreToken() error = nil, want ambiguous output rejected")
	}
}

func openTestService(t *testing.T) *prefs.Service {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return prefs.NewService(store, nil, "")
}
