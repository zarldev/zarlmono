package grpc

import (
	"context"
	"fmt"
	"sort"

	"connectrpc.com/connect"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// ListPromptTemplates returns every code-registered template key with
// its current effective content (override if present, else default).
// Iterates the store's AllKeys so operators see templates that exist
// in code even before they've been edited.
func (a *AdminServer) ListPromptTemplates(ctx context.Context, req *connect.Request[zarlv1.ListPromptTemplatesRequest]) (*connect.Response[zarlv1.ListPromptTemplatesResponse], error) {
	if a.templateStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("prompt templates not configured"))
	}
	keys := a.templateStore.AllKeys()
	sort.Strings(keys)
	out := make([]*zarlv1.PromptTemplateMsg, 0, len(keys))
	for _, k := range keys {
		effective := a.templateStore.Raw(k)
		def := a.templateStore.Default(k)
		out = append(out, &zarlv1.PromptTemplateMsg{
			Key:            k,
			Content:        effective,
			DefaultContent: def,
			HasOverride:    a.templateStore.HasOverride(k),
		})
	}
	return connect.NewResponse(&zarlv1.ListPromptTemplatesResponse{Templates: out}), nil
}

// UpdatePromptTemplate persists an override for a key and mutates the
// in-memory store so the next render picks it up. Rejects keys that
// aren't code-registered — stops operators creating orphan rows for
// templates no code reads.
func (a *AdminServer) UpdatePromptTemplate(ctx context.Context, req *connect.Request[zarlv1.UpdatePromptTemplateRequest]) (*connect.Response[zarlv1.UpdatePromptTemplateResponse], error) {
	if a.templateStore == nil || a.promptTemplates == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("prompt templates not configured"))
	}
	key := req.Msg.Key
	if key == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("key is required"))
	}
	if a.templateStore.Default(key) == "" && !a.templateStore.HasOverride(key) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown template key %q", key))
	}
	if err := a.promptTemplates.Upsert(ctx, key, req.Msg.Content); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	a.templateStore.SetOverride(key, req.Msg.Content)
	return connect.NewResponse(&zarlv1.UpdatePromptTemplateResponse{}), nil
}

// ResetPromptTemplate drops the override row for a key so the code
// default takes effect again. Idempotent — no error when the key has
// no override.
func (a *AdminServer) ResetPromptTemplate(ctx context.Context, req *connect.Request[zarlv1.ResetPromptTemplateRequest]) (*connect.Response[zarlv1.ResetPromptTemplateResponse], error) {
	if a.templateStore == nil || a.promptTemplates == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("prompt templates not configured"))
	}
	if err := a.promptTemplates.Delete(ctx, req.Msg.Key); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	a.templateStore.ClearOverride(req.Msg.Key)
	return connect.NewResponse(&zarlv1.ResetPromptTemplateResponse{}), nil
}
