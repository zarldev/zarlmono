package grpc

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Prompts — CRUD over named system prompt versions, plus Dolt-backed history
// (diff/revert) for every committed change.

func (a *AdminServer) ListPrompts(ctx context.Context, req *connect.Request[zarlv1.ListPromptsRequest]) (*connect.Response[zarlv1.ListPromptsResponse], error) {
	prompts, err := a.prompts.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list prompts: %w", err))
	}
	msgs := make([]*zarlv1.PromptMsg, len(prompts))
	for i, p := range prompts {
		msgs[i] = &zarlv1.PromptMsg{
			Id:      string(p.ID),
			Name:    p.Name,
			Content: p.Content,
			Active:  p.Active,
		}
	}
	return connect.NewResponse(&zarlv1.ListPromptsResponse{Prompts: msgs}), nil
}

func (a *AdminServer) CreatePrompt(ctx context.Context, req *connect.Request[zarlv1.CreatePromptRequest]) (*connect.Response[zarlv1.CreatePromptResponse], error) {
	p, err := a.prompts.Create(ctx, req.Msg.Name, req.Msg.Content)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create prompt: %w", err))
	}
	return connect.NewResponse(&zarlv1.CreatePromptResponse{
		Prompt: &zarlv1.PromptMsg{
			Id:      string(p.ID),
			Name:    p.Name,
			Content: p.Content,
			Active:  p.Active,
		},
	}), nil
}

func (a *AdminServer) UpdatePrompt(ctx context.Context, req *connect.Request[zarlv1.UpdatePromptRequest]) (*connect.Response[zarlv1.UpdatePromptResponse], error) {
	if err := a.prompts.UpdateContent(ctx, repository.PromptID(req.Msg.Id), req.Msg.Content); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update prompt: %w", err))
	}
	active, err := a.prompts.GetActive(ctx)
	if err == nil && string(active.ID) == req.Msg.Id {
		a.zarlServer.Reconfigure(WithSystemPrompt(active.Content))
	}
	return connect.NewResponse(&zarlv1.UpdatePromptResponse{}), nil
}

func (a *AdminServer) SetActivePrompt(ctx context.Context, req *connect.Request[zarlv1.SetActivePromptRequest]) (*connect.Response[zarlv1.SetActivePromptResponse], error) {
	if err := a.prompts.SetActive(ctx, repository.PromptID(req.Msg.Id)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("set active: %w", err))
	}
	active, err := a.prompts.GetActive(ctx)
	if err == nil {
		a.zarlServer.Reconfigure(WithSystemPrompt(active.Content))
		a.emitConfigChange(fmt.Sprintf("System prompt switched to: %s", active.Name))
	}
	return connect.NewResponse(&zarlv1.SetActivePromptResponse{}), nil
}

func (a *AdminServer) DeletePrompt(ctx context.Context, req *connect.Request[zarlv1.DeletePromptRequest]) (*connect.Response[zarlv1.DeletePromptResponse], error) {
	if err := a.prompts.Delete(ctx, repository.PromptID(req.Msg.Id)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete prompt: %w", err))
	}
	return connect.NewResponse(&zarlv1.DeletePromptResponse{}), nil
}

// ── Prompt History (Dolt) ──

func (a *AdminServer) ListPromptHistory(ctx context.Context, req *connect.Request[zarlv1.ListPromptHistoryRequest]) (*connect.Response[zarlv1.ListPromptHistoryResponse], error) {
	commits, err := a.dolt.Log(ctx, 50)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var msgs []*zarlv1.DoltCommitMsg
	for _, c := range commits {
		msgs = append(msgs, &zarlv1.DoltCommitMsg{
			Hash:      c.Hash,
			Committer: c.Committer,
			Message:   c.Message,
			Date:      c.Date,
		})
	}
	return connect.NewResponse(&zarlv1.ListPromptHistoryResponse{Commits: msgs}), nil
}

func (a *AdminServer) DiffPrompt(ctx context.Context, req *connect.Request[zarlv1.DiffPromptRequest]) (*connect.Response[zarlv1.DiffPromptResponse], error) {
	diffs, err := a.dolt.DiffPrompts(ctx, req.Msg.FromHash, req.Msg.ToHash)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var msgs []*zarlv1.PromptDiffMsg
	for _, d := range diffs {
		msgs = append(msgs, &zarlv1.PromptDiffMsg{
			DiffType:    d.DiffType,
			FromContent: d.FromContent,
			ToContent:   d.ToContent,
		})
	}
	return connect.NewResponse(&zarlv1.DiffPromptResponse{Diffs: msgs}), nil
}

func (a *AdminServer) RevertPrompt(ctx context.Context, req *connect.Request[zarlv1.RevertPromptRequest]) (*connect.Response[zarlv1.RevertPromptResponse], error) {
	content, err := a.dolt.RevertPrompt(ctx, req.Msg.CommitHash, req.Msg.PromptId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := a.prompts.UpdateContent(ctx, repository.PromptID(req.Msg.PromptId), content); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	a.zarlServer.Reconfigure(WithSystemPrompt(content))
	return connect.NewResponse(&zarlv1.RevertPromptResponse{Content: content}), nil
}
