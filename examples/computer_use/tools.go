package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func execute[Out any, Args any](ctx context.Context, reg *tools.Registry, name tools.ToolName, args Args) (Out, error) {
	var zero Out
	tool, ok := reg.Tool(name)
	if !ok {
		return zero, fmt.Errorf("tool %q not registered", name)
	}
	params, err := toToolParameters(args)
	if err != nil {
		return zero, fmt.Errorf("encode %q args: %w", name, err)
	}
	res, err := tool.Execute(ctx, tools.ToolCall{ID: tools.ToolCallID(name.String() + "-call"), ToolName: name, Arguments: params})
	if err != nil {
		return zero, err
	}
	if res == nil || !res.Success {
		return zero, fmt.Errorf("tool %q: %s", name, res.Error)
	}
	out, ok := res.Data.(Out)
	if !ok {
		return zero, fmt.Errorf("tool %q: unexpected result type %T, want %T", name, res.Data, out)
	}
	return out, nil
}

func toToolParameters(args any) (tools.ToolParameters, error) {
	by, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var params tools.ToolParameters
	if err := json.Unmarshal(by, &params); err != nil {
		return nil, err
	}
	if params == nil {
		params = tools.ToolParameters{}
	}
	return params, nil
}
