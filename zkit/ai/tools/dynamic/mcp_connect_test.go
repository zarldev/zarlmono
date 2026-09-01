package dynamic_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

type mcpConnectStubTool struct{ name tools.ToolName }

func (s mcpConnectStubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name, Description: "stub", Parameters: llm.Schema{Type: "object"}}
}

func (s mcpConnectStubTool) Execute(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(c.ID, "stub"), nil
}

func buildMCPDiscoveryServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain available: %v", err)
	}
	const src = `package main
import ("bufio"; "encoding/json"; "fmt"; "os"; "strconv"; "strings")
type req struct { JSONRPC string ` + "`json:\"jsonrpc\"`" + `; ID int ` + "`json:\"id\"`" + `; Method string ` + "`json:\"method\"`" + ` }
func main() {
 mode := os.Args[1]; scan := bufio.NewScanner(os.Stdin); enc := json.NewEncoder(os.Stdout)
 for scan.Scan() { var r req; if json.Unmarshal(scan.Bytes(), &r) != nil || r.ID == 0 { continue }
  if r.Method == "initialize" { _ = enc.Encode(map[string]any{"jsonrpc":"2.0", "id":r.ID, "result":map[string]any{"protocolVersion":"2024-11-05", "capabilities":map[string]any{}, "serverInfo":map[string]any{"name":"test", "version":"0"}}}); continue }
  defs := []map[string]any{}
  add := func(name, desc string, schema map[string]any) { defs = append(defs, map[string]any{"name":name, "description":desc, "inputSchema":schema}) }
  switch mode { case "echo": add("echo", "stub", map[string]any{"type":"object"}); case "bash": add("bash", "stub", map[string]any{"type":"object"}); case "duplicate": add("echo", "stub", map[string]any{"type":"object"}); add("echo", "stub", map[string]any{"type":"object"}); case "invalid": add("Bad-Name", "stub", map[string]any{"type":"object"}); case "many": for i:=0;i<65;i++ { add(fmt.Sprintf("tool_%d", i), "stub", map[string]any{"type":"object"}) }; case "description": add("big_description", strings.Repeat("x", 2049), map[string]any{"type":"object"}); case "schema": add("big_schema", "stub", map[string]any{"blob":strings.Repeat("x", 32768)}); default: _, _ = strconv.Atoi(mode) }
  _ = enc.Encode(map[string]any{"jsonrpc":"2.0", "id":r.ID, "result":map[string]any{"tools":defs}})
 }
}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write server: %v", err)
	}
	bin := filepath.Join(dir, "server")
	out, err := exec.Command("go", "build", "-o", bin, filepath.Join(dir, "main.go")).CombinedOutput()
	if err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}
	return bin
}

func executeMCPConnect(t *testing.T, reg *tools.Registry, name, command, mode string) *tools.ToolResult {
	t.Helper()
	mcpReg := dynamic.NewMCPRegistry(reg, nil)
	t.Cleanup(func() { _ = mcpReg.CloseAll() })
	result, err := dynamic.NewMCPConnect(mcpReg).Execute(t.Context(), tools.ToolCall{
		ID: "connect", Arguments: tools.ToolParameters{
			"name": name, "transport": "stdio", "command": command, "args": []string{mode},
		},
	})
	if err != nil {
		t.Fatalf("mcp_connect: %v", err)
	}
	return result
}

func TestMCPConnectValidatesDiscoveredTools(t *testing.T) {
	server := buildMCPDiscoveryServer(t)
	tests := []struct {
		name    string
		mode    string
		setup   func(*tools.Registry)
		wantOK  bool
		wantErr string
	}{
		{name: "rejects shadowing", mode: "bash", setup: func(r *tools.Registry) { r.Register(mcpConnectStubTool{name: "bash"}) }, wantErr: "would shadow existing tool"},
		{name: "rejects duplicate names", mode: "duplicate", wantErr: "duplicate tool name"},
		{name: "rejects invalid name", mode: "invalid", wantErr: "invalid name"},
		{name: "allows unique names", mode: "echo", wantOK: true},
		{name: "caps tool count", mode: "many", wantErr: "max"},
		{name: "caps descriptions", mode: "description", wantErr: "description too large"},
		{name: "caps schemas", mode: "schema", wantErr: "schema too large"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			if tt.setup != nil {
				tt.setup(reg)
			}
			result := executeMCPConnect(t, reg, fmt.Sprintf("conn%d", i), server, tt.mode)
			if result.Success != tt.wantOK {
				t.Fatalf("Success = %v, error = %q, want success %v", result.Success, result.Error, tt.wantOK)
			}
			if tt.wantErr != "" && !strings.Contains(result.Error, tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", result.Error, tt.wantErr)
			}
			if tt.wantOK {
				if _, ok := reg.Tool("echo"); !ok {
					t.Fatal("discovered echo tool was not registered")
				}
			}
		})
	}
}

func TestMCPRegistryConnectPolicyRunsBeforeTransport(t *testing.T) {
	t.Parallel()
	mcpReg := dynamic.NewMCPRegistry(tools.NewRegistry(), nil)
	mcpReg.SetConnectPolicy(dynamic.MCPConnectPolicyFunc(func(_ context.Context, name string, conn dynamic.MCPConnSpec) error {
		if name != "blocked" || conn.Type != dynamic.Transports.TRANSPORTSTDIO {
			t.Fatalf("policy saw name=%q type=%s", name, conn.Type)
		}
		return errors.New("blocked by policy")
	}))
	result, err := dynamic.NewMCPConnect(mcpReg).Execute(t.Context(), tools.ToolCall{ID: "connect", Arguments: tools.ToolParameters{"name": "blocked", "transport": "stdio", "command": "/does/not/exist"}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || !strings.Contains(result.Error, "blocked by policy") {
		t.Fatalf("result = %+v, want policy rejection", result)
	}
}

func TestDefaultMCPPolicyRejectsRelativeAndShellStdio(t *testing.T) {
	t.Parallel()
	if err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(t.Context(), "rel", dynamic.NewStdioMCPConn("server", nil, nil)); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative command err = %v, want absolute path rejection", err)
	}
	shell := filepath.Join(t.TempDir(), "sh")
	if err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(t.Context(), "shell", dynamic.NewStdioMCPConn(shell, nil, nil)); err == nil || !strings.Contains(err.Error(), "is a shell") {
		t.Fatalf("shell command err = %v, want shell rejection", err)
	}
	server := filepath.Join(t.TempDir(), "server")
	if err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(t.Context(), "ok", dynamic.NewStdioMCPConn(server, nil, nil)); err != nil {
		t.Fatalf("non-shell absolute command rejected: %v", err)
	}
}
