package openaicodex_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestDebugSSEDumpsArePrivateIncludingExistingFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission assertion; Windows ACLs are managed by the operator")
	}
	path := filepath.Join(t.TempDir(), "sensitive-events.log")
	if err := os.WriteFile(path, []byte("old data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_DEBUG_SSE", path)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"private-canary\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	t.Cleanup(server.Close)
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "fixture")}, openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
	for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("sensitive log permissions = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "private-canary") {
		t.Fatalf("debug fixture did not write events: %v", err)
	}
}
