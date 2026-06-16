package grpc

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Agent-initiated proposals — new MCP tool providers and system-prompt
// rewrites queued for human review.

func (a *AdminServer) ListToolProposals(ctx context.Context, req *connect.Request[zarlv1.ListToolProposalsRequest]) (*connect.Response[zarlv1.ListToolProposalsResponse], error) {
	proposals, err := a.proposals.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var msgs []*zarlv1.ToolProposalMsg
	for _, p := range proposals {
		msgs = append(msgs, &zarlv1.ToolProposalMsg{
			Id:          p.ID,
			ToolName:    p.ToolName,
			Description: p.Description,
			McpUrl:      p.MCPURL,
			Rationale:   p.Rationale,
			Status:      p.Status,
			CreatedAt:   p.CreatedAt,
		})
	}
	return connect.NewResponse(&zarlv1.ListToolProposalsResponse{Proposals: msgs}), nil
}

func (a *AdminServer) ReviewToolProposal(ctx context.Context, req *connect.Request[zarlv1.ReviewToolProposalRequest]) (*connect.Response[zarlv1.ReviewToolProposalResponse], error) {
	status := "rejected"
	if req.Msg.Approve {
		status = "approved"
	}

	if err := a.proposals.SetStatus(ctx, req.Msg.Id, status); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if req.Msg.Approve {
		proposal, err := a.proposals.Get(ctx, req.Msg.Id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get proposal: %w", err))
		}
		config := map[string]string{"url": proposal.MCPURL}
		provider, err := a.providers.Create(ctx, proposal.ToolName, "mcp", true, config)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create provider: %w", err))
		}
		// If the endpoint is unreachable we keep the row but disable it so
		// the user can fix the URL in the Tools tab — returning 500 here would
		// leave an enabled-but-broken provider.
		if err := a.toolManager.ReloadByID(ctx, repository.ToolProviderID(provider.ID)); err != nil {
			if disableErr := a.providers.Update(ctx, repository.ToolProviderID(provider.ID), false, config); disableErr != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("reload failed and disable failed: reload=%w disable=%w", err, disableErr))
			}
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("reload provider (left disabled): %w", err))
		}
		if a.notifications != nil {
			a.notifications.Broadcast(znotify.Notification{
				ToolName: "self_improvement",
				Content:  fmt.Sprintf("Task complete: Tool %q (%s) is now installed and available. Its actions are in your tool list — use them when relevant.", proposal.ToolName, proposal.Description),
			})
		}
	}

	return connect.NewResponse(&zarlv1.ReviewToolProposalResponse{Status: status}), nil
}

// ── Prompt Proposals ──

func (a *AdminServer) ListPromptProposals(ctx context.Context, req *connect.Request[zarlv1.ListPromptProposalsRequest]) (*connect.Response[zarlv1.ListPromptProposalsResponse], error) {
	proposals, err := a.promptProposals.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	msgs := make([]*zarlv1.PromptProposalMsg, 0, len(proposals))
	for _, p := range proposals {
		msgs = append(msgs, &zarlv1.PromptProposalMsg{
			Id:              p.ID,
			CurrentPromptId: p.CurrentPromptID,
			ProposedContent: p.ProposedContent,
			Rationale:       p.Rationale,
			Status:          p.Status,
			CreatedAt:       p.CreatedAt,
		})
	}
	return connect.NewResponse(&zarlv1.ListPromptProposalsResponse{Proposals: msgs}), nil
}

func (a *AdminServer) ReviewPromptProposal(ctx context.Context, req *connect.Request[zarlv1.ReviewPromptProposalRequest]) (*connect.Response[zarlv1.ReviewPromptProposalResponse], error) {
	status := "rejected"
	if req.Msg.Approve {
		status = "approved"
	}

	if err := a.promptProposals.SetStatus(ctx, req.Msg.Id, status); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if req.Msg.Approve {
		proposal, err := a.promptProposals.Get(ctx, req.Msg.Id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get prompt proposal: %w", err))
		}
		// Preserve the previous prompt by creating a new version and activating
		// it, rather than overwriting the original's content. The new name is
		// derived from the source prompt so the history stays readable in the
		// admin list ("default", "default v2", "default v3", …).
		baseName := "prompt"
		if base, err := a.prompts.List(ctx); err == nil {
			for _, p := range base {
				if string(p.ID) == proposal.CurrentPromptID {
					baseName = p.Name
					break
				}
			}
		}
		newName := nextPromptVersionName(baseName, func() []string {
			names := []string{}
			if ps, err := a.prompts.List(ctx); err == nil {
				for _, p := range ps {
					names = append(names, p.Name)
				}
			}
			return names
		}())
		created, err := a.prompts.Create(ctx, newName, proposal.ProposedContent)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create prompt version: %w", err))
		}
		if err := a.prompts.SetActive(ctx, created.ID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("activate new prompt: %w", err))
		}
		a.zarlServer.Reconfigure(WithSystemPrompt(proposal.ProposedContent))
		if a.notifications != nil {
			a.notifications.Broadcast(znotify.Notification{
				ToolName: "self_improvement",
				Content:  fmt.Sprintf("Task complete: approved proposal activated as %q. The previous prompt is still available in the admin list.", newName),
			})
		}
	}

	return connect.NewResponse(&zarlv1.ReviewPromptProposalResponse{Status: status}), nil
}

// versionSuffixRe matches a trailing " v2", " v10", etc.
var versionSuffixRe = regexp.MustCompile(` v(\d+)$`)

// nextPromptVersionName returns a unique name derived from baseName that does
// not collide with any entry in existing. "default" → "default v2" → "default
// v3", etc. If baseName already ends in " vN" the numeric part is incremented
// from that as the starting point.
func nextPromptVersionName(baseName string, existing []string) string {
	root := baseName
	start := 2
	if m := versionSuffixRe.FindStringSubmatch(baseName); m != nil {
		root = strings.TrimSuffix(baseName, m[0])
		if n, err := strconv.Atoi(m[1]); err == nil {
			start = n + 1
		}
	}
	taken := make(map[string]bool, len(existing))
	for _, n := range existing {
		taken[n] = true
	}
	for i := start; ; i++ {
		candidate := fmt.Sprintf("%s v%d", root, i)
		if !taken[candidate] {
			return candidate
		}
	}
}
