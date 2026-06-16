package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool providers + registered tool introspection + call history.

func (a *AdminServer) ListToolProviders(ctx context.Context, req *connect.Request[zarlv1.ListToolProvidersRequest]) (*connect.Response[zarlv1.ListToolProvidersResponse], error) {
	providers, err := a.providers.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list providers: %w", err))
	}
	msgs := make([]*zarlv1.ToolProviderMsg, len(providers))
	for i, p := range providers {
		cfgJSON, _ := json.Marshal(p.Config)
		msgs[i] = &zarlv1.ToolProviderMsg{
			Id:        string(p.ID),
			Name:      p.Name,
			Type:      p.Type,
			Enabled:   p.Enabled,
			Config:    string(cfgJSON),
			ToolCount: int32(a.registry.ToolCountForProvider(p.Name)),
		}
	}
	return connect.NewResponse(&zarlv1.ListToolProvidersResponse{Providers: msgs}), nil
}

func (a *AdminServer) ListRegisteredTools(ctx context.Context, req *connect.Request[zarlv1.ListRegisteredToolsRequest]) (*connect.Response[zarlv1.ListRegisteredToolsResponse], error) {
	msgs := make([]*zarlv1.RegisteredToolMsg, 0)

	// Registry tools — iterate unwrapped so we can report the
	// code-default description. The override lookup happens explicitly
	// below so has_override is derived from a single authoritative
	// source (the descStore).
	for _, n := range a.registry.Names() {
		t, ok := a.registry.Tool(n)
		if !ok {
			continue
		}
		msgs = append(msgs, a.buildRegisteredToolMsg(t, a.registry.ProviderFor(t.Definition().Name), "registry"))
	}
	// Action tools (taskrunner): unwrapped, surfaced the same way so
	// the admin UI is a single table of every tool the agent sees.
	for _, t := range a.actionTools {
		msgs = append(msgs, a.buildRegisteredToolMsg(tools.UnwrapDescriptionOverride(t), "", "action"))
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Name < msgs[j].Name })
	return connect.NewResponse(&zarlv1.ListRegisteredToolsResponse{Tools: msgs}), nil
}

// buildRegisteredToolMsg fills the proto message from a code-default
// Tool plus the current override (if any). Keeping this in one place
// keeps the list handler small and lets both tool categories
// (registry, action) share identical field-population rules.
func (a *AdminServer) buildRegisteredToolMsg(t tools.Tool, provider, category string) *zarlv1.RegisteredToolMsg {
	def := t.Definition()
	params := toolParamMsgs(def.Parameters)
	effective := def.Description
	hasOverride := false
	if a.toolDescStore != nil {
		if override, ok := a.toolDescStore.Description(def.Name); ok {
			effective = override
			hasOverride = true
		}
	}
	return &zarlv1.RegisteredToolMsg{
		Name:               def.Name.String(),
		Description:        effective,
		DefaultDescription: def.Description,
		HasOverride:        hasOverride,
		Provider:           provider,
		Category:           category,
		Parameters:         params,
	}
}

// toolParamMsgs flattens a tool's parameter schema (an object schema with
// Properties and Required) into the flat per-parameter proto messages the
// admin UI renders. Schemas with no properties yield no rows — the same
// empty-table the old []Parameter path produced for parameterless tools.
func toolParamMsgs(schema llm.Schema) []*zarlv1.ToolParameterMsg {
	if len(schema.Properties) == 0 {
		return nil
	}
	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}
	params := make([]*zarlv1.ToolParameterMsg, 0, len(schema.Properties))
	for name, prop := range schema.Properties {
		var enum []string
		for _, v := range prop.Enum {
			if s, ok := v.(string); ok {
				enum = append(enum, s)
			}
		}
		params = append(params, &zarlv1.ToolParameterMsg{
			Name:        name,
			Type:        prop.Type,
			Description: prop.Description,
			Required:    required[name],
			EnumValues:  enum,
		})
	}
	return params
}

// UpdateToolDescription persists an operator-authored replacement for a
// tool's Description, mutates the in-memory store (so the next LLM
// tool-spec build picks it up), and bumps the registry version so the
// tool-selector's embedding index regenerates against the new text.
func (a *AdminServer) UpdateToolDescription(ctx context.Context, req *connect.Request[zarlv1.UpdateToolDescriptionRequest]) (*connect.Response[zarlv1.UpdateToolDescriptionResponse], error) {
	name := req.Msg.Name
	desc := req.Msg.Description
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}
	if !a.toolExists(name) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown tool %q", name))
	}
	if a.toolDescRepo == nil || a.toolDescStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("tool description overrides not configured"))
	}
	if err := a.toolDescRepo.Upsert(ctx, name, desc); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("upsert tool description override: %w", err))
	}
	a.toolDescStore.Set(tools.ToolName(name), desc)
	return connect.NewResponse(&zarlv1.UpdateToolDescriptionResponse{}), nil
}

// ResetToolDescription removes any override so the tool's code-default
// description takes effect again.
func (a *AdminServer) ResetToolDescription(ctx context.Context, req *connect.Request[zarlv1.ResetToolDescriptionRequest]) (*connect.Response[zarlv1.ResetToolDescriptionResponse], error) {
	name := req.Msg.Name
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}
	if a.toolDescRepo == nil || a.toolDescStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("tool description overrides not configured"))
	}
	if err := a.toolDescRepo.Delete(ctx, name); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete tool description override: %w", err))
	}
	a.toolDescStore.Delete(tools.ToolName(name))
	return connect.NewResponse(&zarlv1.ResetToolDescriptionResponse{}), nil
}

// PreviewGesture broadcasts a gesture/mood cue to every active
// session's talking-head so the admin UI's playground can audition a
// template visually without running a conversation turn. The payload
// shape matches what the conversation-path gesture tool emits so the
// frontend consumer needs no special case.
func (a *AdminServer) PreviewGesture(ctx context.Context, req *connect.Request[zarlv1.PreviewGestureRequest]) (*connect.Response[zarlv1.PreviewGestureResponse], error) {
	gesture := req.Msg.Gesture
	mood := req.Msg.Mood
	if gesture == "" && mood == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one of gesture or mood is required"))
	}
	if a.notifications == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("notifications not configured"))
	}
	payload, err := json.Marshal(map[string]string{"gesture": gesture, "mood": mood})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal gesture payload: %w", err))
	}
	a.notifications.Push(znotify.Notification{
		ToolName:  "gesture",
		Content:   string(payload),
		Broadcast: true,
	})
	return connect.NewResponse(&zarlv1.PreviewGestureResponse{}), nil
}

// toolExists reports whether a tool with this name is known to the
// admin server — registry or action. Prevents operators from creating
// orphan override rows for tools that don't exist.
func (a *AdminServer) toolExists(name string) bool {
	if _, ok := a.registry.Tool(tools.ToolName(name)); ok {
		return true
	}
	for _, t := range a.actionTools {
		if tools.UnwrapDescriptionOverride(t).Definition().Name.String() == name {
			return true
		}
	}
	return false
}

func (a *AdminServer) UpdateToolProvider(ctx context.Context, req *connect.Request[zarlv1.UpdateToolProviderRequest]) (*connect.Response[zarlv1.UpdateToolProviderResponse], error) {
	var config map[string]string
	if err := json.Unmarshal([]byte(req.Msg.Config), &config); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid config: %w", err))
	}
	if err := a.providers.Update(ctx, repository.ToolProviderID(req.Msg.Id), req.Msg.Enabled, config); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update provider: %w", err))
	}
	if err := a.toolManager.ReloadByID(ctx, repository.ToolProviderID(req.Msg.Id)); err != nil {
		slog.WarnContext(ctx, "reload tool provider", "id", req.Msg.Id, "err", err)
	}
	return connect.NewResponse(&zarlv1.UpdateToolProviderResponse{}), nil
}

func (a *AdminServer) CreateToolProvider(ctx context.Context, req *connect.Request[zarlv1.CreateToolProviderRequest]) (*connect.Response[zarlv1.CreateToolProviderResponse], error) {
	var config map[string]string
	if err := json.Unmarshal([]byte(req.Msg.Config), &config); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid config: %w", err))
	}
	p, err := a.providers.Create(ctx, req.Msg.Name, req.Msg.Type, false, config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create provider: %w", err))
	}
	cfgJSON, _ := json.Marshal(p.Config)
	return connect.NewResponse(&zarlv1.CreateToolProviderResponse{
		Provider: &zarlv1.ToolProviderMsg{
			Id:     string(p.ID),
			Name:   p.Name,
			Type:   p.Type,
			Config: string(cfgJSON),
		},
	}), nil
}

func (a *AdminServer) DeleteToolProvider(ctx context.Context, req *connect.Request[zarlv1.DeleteToolProviderRequest]) (*connect.Response[zarlv1.DeleteToolProviderResponse], error) {
	if err := a.toolManager.UnloadByID(ctx, repository.ToolProviderID(req.Msg.Id)); err != nil {
		slog.WarnContext(ctx, "unload tool provider", "id", req.Msg.Id, "err", err)
	}
	if err := a.providers.Delete(ctx, repository.ToolProviderID(req.Msg.Id)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete provider: %w", err))
	}
	return connect.NewResponse(&zarlv1.DeleteToolProviderResponse{}), nil
}

// ── Tool Call History ──

func (a *AdminServer) ListToolCalls(ctx context.Context, req *connect.Request[zarlv1.ListToolCallsRequest]) (*connect.Response[zarlv1.ListToolCallsResponse], error) {
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 25
	}
	calls, total, err := a.toolCalls.List(ctx, limit, int(req.Msg.Offset))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list tool calls: %w", err))
	}
	msgs := make([]*zarlv1.ToolCallMsg, len(calls))
	for i, c := range calls {
		msgs[i] = &zarlv1.ToolCallMsg{
			Id:         c.ID,
			SessionId:  c.SessionID,
			ToolName:   c.ToolName,
			Provider:   c.Provider,
			Args:       c.Args,
			Result:     c.Result,
			Error:      c.Error,
			DurationMs: int32(c.DurationMs),
			CreatedAt:  c.CreatedAt,
		}
	}
	return connect.NewResponse(&zarlv1.ListToolCallsResponse{Calls: msgs, Total: int32(total)}), nil
}

func (a *AdminServer) GetToolCallStats(ctx context.Context, req *connect.Request[zarlv1.GetToolCallStatsRequest]) (*connect.Response[zarlv1.GetToolCallStatsResponse], error) {
	stats, err := a.toolCalls.Stats(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("tool call stats: %w", err))
	}
	msgs := make([]*zarlv1.ToolCallStatMsg, len(stats))
	for i, s := range stats {
		msgs[i] = &zarlv1.ToolCallStatMsg{
			ToolName:      s.ToolName,
			Provider:      s.Provider,
			TotalCalls:    int32(s.TotalCalls),
			AvgDurationMs: float32(s.AvgDurationMs),
			ErrorCount:    int32(s.ErrorCount),
		}
	}
	return connect.NewResponse(&zarlv1.GetToolCallStatsResponse{Stats: msgs}), nil
}
