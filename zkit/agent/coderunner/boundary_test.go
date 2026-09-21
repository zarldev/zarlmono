package coderunner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestRestrictedMCPManagement(t *testing.T) {
	for _, mode := range []spawn.SpawnMode{spawn.SpawnModeExplore, spawn.SpawnModeVerify} {
		t.Run(string(mode), func(t *testing.T) {
			registry := tools.NewRegistry()
			mcp := dynamic.NewMCPRegistry(registry, nil)
			attempted := false
			mcp.SetConnectPolicy(dynamic.MCPConnectPolicyFunc(func(context.Context, string, dynamic.MCPConnSpec) error { attempted = true; return nil }))
			connect, disconnect := dynamic.NewMCPConnect(mcp), dynamic.NewMCPDisconnect(mcp)
			if connect.Definition().Mutates || disconnect.Definition().Mutates {
				t.Fatal("connection management must not count as a completed file edit")
			}
			for _, tool := range []tools.Tool{connect, disconnect} {
				if err := registry.Register(tool); err != nil {
					t.Fatal(err)
				}
			}
			source, err := sourcechain.New(registry, guardrails.Deps{})
			if err != nil {
				t.Fatal(err)
			}
			policy := coderunner.SpawnModePolicy()
			client := runnertest.NewClient([][]llm.CompletionChunk{
				{runnertest.ChunkToolCall("c", "mcp_connect", `{"name":"blocked","transport":"stdio","command":"/usr/bin/python3","args":["-c","raise Exception()"]}`)},
				{runnertest.ChunkToolCall("d", "mcp_disconnect", `{"name":"blocked"}`)},
				{runnertest.ChunkText("done")},
			})
			sink := &runnertest.Sink{}
			r := runner.New(client, runner.WithTools(source.Source), runner.WithSink(sink))
			_ = r.Run(runner.WithToolGate(t.Context(), func(spec tools.ToolSpec) bool { return policy(mode, spec) }), runner.TaskSpec{Prompt: "inspect", MaxIterations: 3})
			if attempted {
				t.Fatal("restricted connection reached transport policy")
			}
			if sink.ToolFailedCount() != 2 {
				t.Fatalf("failures = %d", sink.ToolFailedCount())
			}
		})
	}
}

func TestNilSandboxDoesNotGrantOutsideReads(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	for _, unrestricted := range []bool{false, true} {
		reg := tools.NewRegistry()
		var opts []coderunner.ToolsOption
		if unrestricted {
			opts = append(opts, coderunner.WithUnrestrictedReads())
		}
		coderunner.RegisterStandardTools(reg, ws, nil, opts...)
		result, err := reg.Execute(t.Context(), tools.ToolCall{ID: "read", ToolName: code.ToolNameRead, Arguments: tools.ToolParameters{"path": outside}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Success != unrestricted {
			t.Fatalf("unrestricted=%v: %+v", unrestricted, result)
		}
	}
}
