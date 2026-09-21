package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestMCPApprovalBindsEntireSpecification(t *testing.T) {
	original := dynamic.MCPConnSpec{Type: dynamic.Transports.TRANSPORTSTDIO, Command: "/usr/bin/python3", Args: []string{"server.py"}, Env: map[string]string{"KEY": "CANARY"}}
	policy := engine.NewMCPConnectPolicy(map[string]dynamic.MCPConnSpec{"approved": original})
	if err := policy.ValidateMCPConnect(t.Context(), "approved", original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*dynamic.MCPConnSpec)
	}{
		{"command", func(s *dynamic.MCPConnSpec) { s.Command = "/usr/bin/env" }},
		{"args", func(s *dynamic.MCPConnSpec) { s.Args = []string{"-c", "unapproved"} }},
		{"environment", func(s *dynamic.MCPConnSpec) { s.Env = map[string]string{"KEY": "changed"} }},
		{"token", func(s *dynamic.MCPConnSpec) { s.AuthToken = "other" }},
		{"endpoint", func(s *dynamic.MCPConnSpec) { s.BaseURL = "https://8.8.8.8/mcp" }},
		{"transport", func(s *dynamic.MCPConnSpec) { s.Type = dynamic.Transports.TRANSPORTHTTP }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposed := original
			tc.change(&proposed)
			if err := policy.ValidateMCPConnect(t.Context(), "approved", proposed); err == nil {
				t.Fatal("changed operation approved")
			}
		})
	}
	if err := policy.ValidateMCPConnect(t.Context(), "unknown", original); err == nil {
		t.Fatal("unknown server approved")
	}
	original.Args[0] = "changed-after-snapshot"
	original.Env["KEY"] = "changed-after-snapshot"
	if err := policy.ValidateMCPConnect(t.Context(), "approved", original); err == nil {
		t.Fatal("approval aliased caller state")
	}
}

func TestMCPApprovalRetainsTransportRestrictions(t *testing.T) {
	for _, spec := range []dynamic.MCPConnSpec{
		{Type: dynamic.Transports.TRANSPORTSTDIO, Command: "/bin/sh"},
		{Type: dynamic.Transports.TRANSPORTHTTP, BaseURL: "http://127.0.0.1/mcp"},
		{Type: dynamic.Transports.TRANSPORTHTTP, BaseURL: "http://8.8.8.8/mcp", AuthToken: "CANARY"},
	} {
		policy := engine.NewMCPConnectPolicy(map[string]dynamic.MCPConnSpec{"approved": spec})
		if err := policy.ValidateMCPConnect(t.Context(), "approved", spec); err == nil {
			t.Fatalf("unsafe approved spec bypassed transport policy: %+v", spec)
		}
	}
}
