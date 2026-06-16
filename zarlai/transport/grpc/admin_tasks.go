package grpc

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Tasks — long-running background research jobs plus the per-provider
// settings that dictate which backend the runner uses.

func (a *AdminServer) ListTasks(ctx context.Context, req *connect.Request[zarlv1.ListTasksRequest]) (*connect.Response[zarlv1.ListTasksResponse], error) {
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 25
	}
	tasks, total, err := a.tasks.List(ctx, limit, int(req.Msg.Offset))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list tasks: %w", err))
	}
	msgs := make([]*zarlv1.TaskMsg, len(tasks))
	for i, t := range tasks {
		msgs[i] = &zarlv1.TaskMsg{
			Id:            t.ID,
			Prompt:        t.Prompt,
			Status:        t.Status,
			Summary:       t.Summary,
			Iterations:    int32(t.Iterations),
			MaxIterations: int32(t.MaxIterations),
			PersonName:    t.PersonName,
			Schedule:      t.Schedule,
			CreatedAt:     t.CreatedAt,
			ProfileName:   t.ProfileName,
			WorkspaceName: t.WorkspaceName,
		}
	}
	return connect.NewResponse(&zarlv1.ListTasksResponse{Tasks: msgs, Total: int32(total)}), nil
}

func (a *AdminServer) CancelTask(ctx context.Context, req *connect.Request[zarlv1.CancelTaskRequest]) (*connect.Response[zarlv1.CancelTaskResponse], error) {
	if err := a.tasks.SetStatus(ctx, repository.TaskID(req.Msg.Id), "failed"); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("cancel task: %w", err))
	}
	return connect.NewResponse(&zarlv1.CancelTaskResponse{}), nil
}

func (a *AdminServer) DeleteTask(ctx context.Context, req *connect.Request[zarlv1.DeleteTaskRequest]) (*connect.Response[zarlv1.DeleteTaskResponse], error) {
	if err := a.tasks.Delete(ctx, repository.TaskID(req.Msg.Id)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete task: %w", err))
	}
	return connect.NewResponse(&zarlv1.DeleteTaskResponse{}), nil
}

// ── Task Provider Settings ──

func (a *AdminServer) GetTaskProviderSettings(ctx context.Context, req *connect.Request[zarlv1.GetTaskProviderSettingsRequest]) (*connect.Response[zarlv1.GetTaskProviderSettingsResponse], error) {
	provider, _ := a.settings.Get(ctx, "task_provider")
	if provider == "" {
		provider = "ollama"
	}
	model, _ := a.settings.Get(ctx, "task_provider_model")
	baseURL, _ := a.settings.Get(ctx, "task_provider_base")
	apiKey, _ := a.settings.Get(ctx, "task_provider_key")
	budgetStr, _ := a.settings.Get(ctx, "task_context_budget")

	budget := int32(40000)
	if budgetStr != "" {
		var b int
		if _, err := fmt.Sscanf(budgetStr, "%d", &b); err == nil && b > 0 {
			budget = int32(b)
		}
	}

	return connect.NewResponse(&zarlv1.GetTaskProviderSettingsResponse{
		Provider:      provider,
		Model:         model,
		BaseUrl:       baseURL,
		ApiKeyMasked:  maskKey(apiKey),
		ContextBudget: budget,
	}), nil
}

func (a *AdminServer) UpdateTaskProviderSettings(ctx context.Context, req *connect.Request[zarlv1.UpdateTaskProviderSettingsRequest]) (*connect.Response[zarlv1.UpdateTaskProviderSettingsResponse], error) {
	provider := req.Msg.Provider
	model := req.Msg.Model
	baseURL := req.Msg.BaseUrl
	apiKey := req.Msg.ApiKey
	budget := int(req.Msg.ContextBudget)
	if budget <= 0 {
		budget = 40000
	}

	if err := a.settings.Set(ctx, "task_provider", provider); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist task_provider: %w", err))
	}
	if err := a.settings.Set(ctx, "task_provider_model", model); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist task_provider_model: %w", err))
	}
	if err := a.settings.Set(ctx, "task_provider_base", baseURL); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist task_provider_base: %w", err))
	}
	if apiKey != "" {
		if err := a.settings.Set(ctx, "task_provider_key", apiKey); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist task_provider_key: %w", err))
		}
	}
	if err := a.settings.Set(ctx, "task_context_budget", fmt.Sprintf("%d", budget)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist task_context_budget: %w", err))
	}

	// Fall back to the stored key when the request omits one (edit without
	// re-entering the secret).
	if apiKey == "" {
		apiKey, _ = a.settings.Get(ctx, "task_provider_key")
	}

	chatClient, err := buildChatClient(provider, baseURL, apiKey, model)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	a.runner.Reconfigure(
		taskrunner.WithChatClient(chatClient),
		taskrunner.WithContextBudget(budget),
	)
	a.emitConfigChange(fmt.Sprintf("Task runner LLM changed to %s / %s", provider, model))

	return connect.NewResponse(&zarlv1.UpdateTaskProviderSettingsResponse{}), nil
}
