package mcp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

// buildFakeStdioServer compiles the tiny test server under
// ./testdata/stdiosrv and returns the resulting binary path. The binary
// speaks enough of the MCP protocol (initialize, tools/list, tools/call)
// to round-trip through the real stdioTransport.
func buildFakeStdioServer(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "stdiosrv", "main.go")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("test fixture missing: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "stdiosrv")
	out, err := exec.Command("go", "build", "-o", bin, "./testdata/stdiosrv").CombinedOutput()
	if err != nil {
		t.Fatalf("build fake server: %v\n%s", err, out)
	}
	return bin
}

func TestStdioTransportDiscoverAndCall(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	bin := buildFakeStdioServer(t)

	c, err := mcp.NewStdioClient(bin, nil, nil)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	defs, err := c.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("discovered = %+v, want single echo tool", defs)
	}

	got, err := c.Call(t.Context(), defs[0].Name, map[string]any{"message": "hello stdio"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	text := got.FirstText()
	if !strings.Contains(text, "hello stdio") {
		t.Errorf("first text = %q, want it to contain %q", text, "hello stdio")
	}
}

func TestStdioTransportDoesNotInheritParentEnvironment(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	const key = "ZARL_MCP_STDIO_SECRET_SHOULD_NOT_LEAK"
	t.Setenv(key, "super-secret")
	bin := buildFakeStdioServer(t)

	c, err := mcp.NewStdioClient(bin, nil, nil)
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.Call(t.Context(), "echo", map[string]any{"message": "env:" + key})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if text := got.FirstText(); text != "echo: " {
		t.Fatalf("child saw parent secret env; first text = %q", text)
	}
}

func TestStdioTransportPassesExplicitEnvironment(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	const key = "ZARL_MCP_STDIO_EXPLICIT_ENV"
	bin := buildFakeStdioServer(t)

	c, err := mcp.NewStdioClient(bin, nil, map[string]string{key: "allowed"})
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	got, err := c.Call(t.Context(), "echo", map[string]any{"message": "env:" + key})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if text := got.FirstText(); text != "echo: allowed" {
		t.Fatalf("first text = %q, want explicit env value", text)
	}
}
