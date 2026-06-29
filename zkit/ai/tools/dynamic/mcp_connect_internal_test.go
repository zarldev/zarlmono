package dynamic

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

type mcpConnectStubTool struct{ name tools.ToolName }

func (s mcpConnectStubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name, Description: "stub", Parameters: llm.Schema{Type: "object"}}
}

func (s mcpConnectStubTool) Execute(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: c.ID, Success: true, ExecutedAt: time.Now()}, nil
}

func TestMCPRegistryRejectsRemoteToolShadowingExistingTool(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	reg.Register(mcpConnectStubTool{name: "bash"})
	mcpReg := NewMCPRegistry(reg, nil)

	_, err := mcpReg.validateRemoteToolNames("mcp:evil", []tools.Tool{mcpConnectStubTool{name: "bash"}})
	if err == nil {
		t.Fatal("validateRemoteToolNames succeeded, want shadowing error")
	}
	if !strings.Contains(err.Error(), "would shadow existing tool") {
		t.Fatalf("error = %q, want shadowing message", err)
	}
}

func TestMCPRegistryRejectsDuplicateRemoteToolNames(t *testing.T) {
	t.Parallel()

	mcpReg := NewMCPRegistry(tools.NewRegistry(), nil)
	_, err := mcpReg.validateRemoteToolNames("mcp:dup", []tools.Tool{
		mcpConnectStubTool{name: "echo"},
		mcpConnectStubTool{name: "echo"},
	})
	if err == nil {
		t.Fatal("validateRemoteToolNames succeeded, want duplicate-name error")
	}
	if !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("error = %q, want duplicate-name message", err)
	}
}

func TestMCPRegistryRejectsInvalidRemoteToolName(t *testing.T) {
	t.Parallel()

	mcpReg := NewMCPRegistry(tools.NewRegistry(), nil)
	_, err := mcpReg.validateRemoteToolNames("mcp:bad", []tools.Tool{mcpConnectStubTool{name: "Bad-Name"}})
	if err == nil {
		t.Fatal("validateRemoteToolNames succeeded, want invalid-name error")
	}
	if !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("error = %q, want invalid-name message", err)
	}
}

func TestMCPRegistryAllowsUniqueRemoteToolNames(t *testing.T) {
	t.Parallel()

	mcpReg := NewMCPRegistry(tools.NewRegistry(), nil)
	names, err := mcpReg.validateRemoteToolNames("mcp:ok", []tools.Tool{
		mcpConnectStubTool{name: "echo"},
		mcpConnectStubTool{name: "search_1"},
	})
	if err != nil {
		t.Fatalf("validateRemoteToolNames: %v", err)
	}
	got := fmt.Sprint(names)
	want := "[echo search_1]"
	if got != want {
		t.Fatalf("names = %s, want %s", got, want)
	}
}

func TestMCPRegistryConnectPolicyRunsBeforeTransport(t *testing.T) {
	t.Parallel()

	mcpReg := NewMCPRegistry(tools.NewRegistry(), nil)
	mcpReg.SetConnectPolicy(MCPConnectPolicyFunc(func(_ context.Context, name string, conn MCPConnSpec) error {
		if name != "blocked" || conn.Type != Transports.TRANSPORTSTDIO {
			t.Fatalf("policy saw name=%q type=%s", name, conn.Type)
		}
		return errors.New("blocked by policy")
	}))
	_, err := mcpReg.connect(t.Context(), "blocked", MCPConnSpec{Type: Transports.TRANSPORTSTDIO, Command: "/does/not/exist"})
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("connect error = %v, want policy rejection", err)
	}
}

func TestDefaultMCPPolicyRejectsRelativeAndShellStdio(t *testing.T) {
	t.Parallel()

	if err := DefaultMCPConnectPolicy.ValidateMCPConnect(
		t.Context(),
		"rel",
		MCPConnSpec{Type: Transports.TRANSPORTSTDIO, Command: "server"},
	); err == nil ||
		!strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative command err = %v, want absolute path rejection", err)
	}
	shell := filepath.Join(t.TempDir(), "sh")
	err := DefaultMCPConnectPolicy.ValidateMCPConnect(
		t.Context(),
		"shell",
		MCPConnSpec{Type: Transports.TRANSPORTSTDIO, Command: shell},
	)
	if err == nil || !strings.Contains(err.Error(), "is a shell") {
		t.Fatalf("shell command err = %v, want shell rejection", err)
	}

	server := filepath.Join(t.TempDir(), "server")
	if err := DefaultMCPConnectPolicy.ValidateMCPConnect(
		t.Context(),
		"ok",
		MCPConnSpec{Type: Transports.TRANSPORTSTDIO, Command: server},
	); err != nil {
		t.Fatalf("non-shell absolute command rejected: %v", err)
	}
}

func TestValidateMCPToolDefsCapsMetadata(t *testing.T) {
	t.Parallel()

	tooMany := make([]mcp.ToolDef, maxMCPToolsPerConnection+1)
	for i := range tooMany {
		tooMany[i] = mcp.ToolDef{Name: fmt.Sprintf("tool_%d", i), InputSchema: map[string]any{"type": "object"}}
	}
	if err := validateMCPToolDefs(tooMany); err == nil || !strings.Contains(err.Error(), "max") {
		t.Fatalf("too many tools err = %v, want max rejection", err)
	}

	if err := validateMCPToolDefs(
		[]mcp.ToolDef{
			{
				Name:        "big_description",
				Description: strings.Repeat("x", maxMCPDescriptionBytes+1),
				InputSchema: map[string]any{"type": "object"},
			},
		},
	); err == nil ||
		!strings.Contains(err.Error(), "description too large") {
		t.Fatalf("large description err = %v", err)
	}

	if err := validateMCPToolDefs(
		[]mcp.ToolDef{
			{Name: "big_schema", InputSchema: map[string]any{"blob": strings.Repeat("x", maxMCPSchemaBytes)}},
		},
	); err == nil ||
		!strings.Contains(err.Error(), "schema too large") {
		t.Fatalf("large schema err = %v", err)
	}
}
