package board_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	domain "github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolsBindAuthorityAndValidateBounds(t *testing.T) {
	goal := swarm.New("goal")
	t.Cleanup(func() { _ = goal.Close(t.Context()) })
	member, err := goal.Add(swarm.Agent{ID: "researcher"})
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	if err := boardtools.Register(reg, member); err != nil {
		t.Fatal(err)
	}
	post, _ := reg.Tool(boardtools.PostName)
	result, err := post.Execute(taskscope.WithID(t.Context(), "child-run"), tools.ToolCall{ID: "post", Arguments: tools.ToolParameters{
		"summary": "finding", "body": "full evidence", "author": "forged", "goal": "foreign",
	}})
	if err != nil || !result.Success {
		t.Fatalf("post = %+v, %v", result, err)
	}
	metadata := result.Data.(domain.Summary)
	if metadata.Author != "agent/researcher" || metadata.ID != 1 {
		t.Fatalf("untrusted identity replaced host binding: %+v", metadata)
	}
	list, _ := reg.Tool(boardtools.ListName)
	listed, err := list.Execute(t.Context(), tools.ToolCall{ID: "list", Arguments: tools.ToolParameters{}})
	if err != nil || !listed.Success {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	encoded, _ := json.Marshal(listed.Data)
	if strings.Contains(string(encoded), "full evidence") {
		t.Fatal("list leaked body into metadata")
	}
	read, _ := reg.Tool(boardtools.ReadName)
	readResult, err := read.Execute(t.Context(), tools.ToolCall{ID: "read", Arguments: tools.ToolParameters{"ids": []int{1}}})
	if err != nil || !readResult.Success || readResult.Data.([]domain.Post)[0].Body != "full evidence" {
		t.Fatalf("read = %+v, %v", readResult, err)
	}
	for _, call := range []struct {
		tool tools.Tool
		args tools.ToolParameters
	}{
		{post, tools.ToolParameters{"summary": "bad", "body": ""}},
		{post, tools.ToolParameters{"summary": "bad", "body": "body", "reply_to": -1}},
		{list, tools.ToolParameters{"limit": 33}},
		{read, tools.ToolParameters{"ids": []int{999}}},
	} {
		result, err := call.tool.Execute(t.Context(), tools.ToolCall{ID: "invalid", Arguments: call.args})
		if err != nil || result.Success {
			t.Fatalf("invalid arguments not recoverable: %+v, %v", result, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := post.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"summary": "cancelled", "body": "body"}}); err == nil && result.Success {
		t.Fatal("cancelled post accepted")
	}
}

func TestToolsDoNotGrantWorkspaceAuthority(t *testing.T) {
	goal := swarm.New("goal")
	defer func() { _ = goal.Close(t.Context()) }()
	member, _ := goal.Add(swarm.Agent{ID: "researcher"})
	reg := tools.NewRegistry()
	if err := boardtools.Register(reg, member); err != nil {
		t.Fatal(err)
	}
	for _, spec := range reg.ToolSpecs() {
		if spec.ChangesWorkspace() || spec.WorkspaceAccess != tools.WorkspaceAccesses.NONE {
			t.Fatalf("collaboration claims file/process authority: %+v", spec)
		}
	}
}
