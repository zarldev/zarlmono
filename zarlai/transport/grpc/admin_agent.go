package grpc

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Agent name — the display name the agent uses for itself. Persisted in the
// settings table and injected into any prompt that references {{agent_name}}.
// A separate spoken_name setting lets TTS substitute a phonetic spelling while
// keeping the display name unchanged in text output.

const agentNameSettingKey = "agent_name"
const agentSpokenNameSettingKey = "agent_spoken_name"
const agentAvatarSettingKey = "agent_avatar"

func (a *AdminServer) GetAgentName(ctx context.Context, req *connect.Request[zarlv1.GetAgentNameRequest]) (*connect.Response[zarlv1.GetAgentNameResponse], error) {
	display, _ := a.settings.Get(ctx, agentNameSettingKey)
	if display == "" {
		display = service.DefaultAgentName
	}
	spoken, _ := a.settings.Get(ctx, agentSpokenNameSettingKey)
	if spoken == "" {
		spoken = display
	}
	return connect.NewResponse(&zarlv1.GetAgentNameResponse{
		DisplayName: display,
		SpokenName:  spoken,
	}), nil
}

func (a *AdminServer) SetAgentName(ctx context.Context, req *connect.Request[zarlv1.SetAgentNameRequest]) (*connect.Response[zarlv1.SetAgentNameResponse], error) {
	display := strings.TrimSpace(req.Msg.DisplayName)
	if display == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("display_name must not be empty"))
	}
	if err := a.settings.Set(ctx, agentNameSettingKey, display); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist agent name: %w", err))
	}

	// Store empty string when spoken == display so the TTS substitution is
	// skipped (no-op cost); store the trimmed value when they differ.
	spoken := strings.TrimSpace(req.Msg.SpokenName)
	if spoken == display {
		spoken = ""
	}
	if err := a.settings.Set(ctx, agentSpokenNameSettingKey, spoken); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist agent spoken name: %w", err))
	}

	if a.zarlServer != nil {
		a.zarlServer.Reconfigure(WithAgentName(display))
	}
	if a.runner != nil {
		a.runner.Reconfigure(taskrunner.WithAgentName(display))
	}
	return connect.NewResponse(&zarlv1.SetAgentNameResponse{}), nil
}

func (a *AdminServer) GetAgentAvatar(ctx context.Context, req *connect.Request[zarlv1.GetAgentAvatarRequest]) (*connect.Response[zarlv1.GetAgentAvatarResponse], error) {
	avatarID, _ := a.settings.Get(ctx, agentAvatarSettingKey)
	return connect.NewResponse(&zarlv1.GetAgentAvatarResponse{AvatarId: avatarID}), nil
}

func (a *AdminServer) SetAgentAvatar(ctx context.Context, req *connect.Request[zarlv1.SetAgentAvatarRequest]) (*connect.Response[zarlv1.SetAgentAvatarResponse], error) {
	avatarID := strings.TrimSpace(req.Msg.AvatarId)
	if err := a.settings.Set(ctx, agentAvatarSettingKey, avatarID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist agent avatar: %w", err))
	}
	return connect.NewResponse(&zarlv1.SetAgentAvatarResponse{}), nil
}
