package dynamic_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestMCPAllowlistAllowsOnlyExplicitLoopback(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:9191/", "http://localhost:9191/", "http://LOCALHOST.:9191/",
		"http://[::1]:9191/", "http://[::ffff:127.0.0.1]:9191/",
	} {
		t.Run(endpoint, func(t *testing.T) {
			spec := dynamic.NewHTTPMCPConn(endpoint, "")
			policy := dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{"blender": spec})
			if err := policy.ValidateMCPConnect(t.Context(), "blender", spec); err != nil {
				t.Fatalf("exact approved loopback: %v", err)
			}
			if err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(t.Context(), "blender", spec); err == nil {
				t.Fatal("default policy allowed loopback")
			}
			var denied *tools.Error
			if err := policy.ValidateMCPConnect(t.Context(), "unknown", spec); !errors.As(err, &denied) || denied.Kind != tools.Kinds.PERMISSION {
				t.Fatalf("unknown connection: %v", err)
			}
		})
	}
}

func TestMCPAllowlistBindsHTTPEndpointAndCredentials(t *testing.T) {
	original := dynamic.NewHTTPMCPConn("https://127.0.0.1:9191/mcp", "CANARY")
	approved := map[string]dynamic.MCPConnSpec{"blender": original}
	policy := dynamic.NewMCPAllowlistPolicy(approved)
	delete(approved, "blender")
	if err := policy.ValidateMCPConnect(t.Context(), "blender", original); err != nil {
		t.Fatalf("snapshot changed with caller map: %v", err)
	}
	for _, spec := range []dynamic.MCPConnSpec{
		dynamic.NewHTTPMCPConn("https://127.0.0.1:9192/mcp", "CANARY"),
		dynamic.NewHTTPMCPConn("https://127.0.0.1:9191/other", "CANARY"),
		dynamic.NewHTTPMCPConn("https://localhost:9191/mcp", "CANARY"),
		dynamic.NewHTTPMCPConn("http://127.0.0.1:9191/mcp", "CANARY"),
		dynamic.NewHTTPMCPConn(original.BaseURL, "OTHER_CANARY"),
		dynamic.NewHTTPMCPConn(original.BaseURL, ""),
	} {
		var denied *tools.Error
		err := policy.ValidateMCPConnect(t.Context(), "blender", spec)
		if !errors.As(err, &denied) || denied.Kind != tools.Kinds.PERMISSION {
			t.Fatalf("changed connection not denied: %v", err)
		}
		if strings.Contains(err.Error(), "CANARY") {
			t.Fatal("policy error leaked credentials")
		}
	}
}

func TestMCPAllowlistRetainsUnsafeURLRejection(t *testing.T) {
	for _, endpoint := range []string{
		"http://192.168.1.5:9191/", "http://10.0.0.1/", "http://172.16.0.1/",
		"http://169.254.169.254/", "http://0.0.0.0/", "http://224.0.0.1/",
		"http://[::]/", "http://[fe80::1]/", "http://[::ffff:169.254.169.254]/",
		"file://localhost/mcp", "http:///mcp", "http://127.0.0.1:bad/",
		"http://CANARY@127.0.0.1/", "http://CANARY%zz@127.0.0.1/",
		"http://127.0.0.1/#fragment",
	} {
		t.Run(endpoint, func(t *testing.T) {
			spec := dynamic.NewHTTPMCPConn(endpoint, "")
			policy := dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{"blender": spec})
			err := policy.ValidateMCPConnect(t.Context(), "blender", spec)
			if err == nil {
				t.Fatal("unsafe URL accepted")
			}
			if strings.Contains(err.Error(), "CANARY") {
				t.Fatal("policy error leaked URL credentials")
			}
		})
	}
}
