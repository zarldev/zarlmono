package grpc

import (
	"context"
	"encoding/json"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"connectrpc.com/connect"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// Chat history (post-session summaries) and sensor proposals — the
// agent-facing-memory surface of the admin UI.

func (a *AdminServer) ListConversationSummaries(ctx context.Context, req *connect.Request[zarlv1.ListConversationSummariesRequest]) (*connect.Response[zarlv1.ListConversationSummariesResponse], error) {
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 50
	}
	offset := max(int(req.Msg.Offset), 0)
	summaries, total, err := a.summaries.ListPaged(ctx, req.Msg.PersonName, limit, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	msgs := make([]*zarlv1.ConversationSummaryMsg, 0, len(summaries))
	for _, s := range summaries {
		msgs = append(msgs, &zarlv1.ConversationSummaryMsg{
			Id:         s.ID,
			PersonName: s.PersonName,
			SessionId:  s.SessionID,
			Summary:    s.Summary,
			CreatedAt:  s.CreatedAt,
		})
	}
	return connect.NewResponse(&zarlv1.ListConversationSummariesResponse{
		Summaries: msgs,
		Total:     int32(total),
	}), nil
}

// ── Sensor proposals ──

func (a *AdminServer) ListSensorProposals(ctx context.Context, _ *connect.Request[zarlv1.ListSensorProposalsRequest]) (*connect.Response[zarlv1.ListSensorProposalsResponse], error) {
	rows, err := a.sensorProposals.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	msgs := make([]*zarlv1.SensorProposalMsg, 0, len(rows))
	for _, p := range rows {
		argsJSON := "{}"
		if b, err := json.Marshal(p.ToolArgs); err == nil {
			argsJSON = string(b)
		}
		msgs = append(msgs, &zarlv1.SensorProposalMsg{
			Id:              p.ID,
			ToolName:        p.ToolName,
			ToolArgsJson:    argsJSON,
			IntervalSeconds: int32(p.IntervalSeconds),
			Rationale:       p.Rationale,
			Status:          p.Status,
			CreatedAt:       p.CreatedAt,
		})
	}
	return connect.NewResponse(&zarlv1.ListSensorProposalsResponse{Proposals: msgs}), nil
}

func (a *AdminServer) ReviewSensorProposal(ctx context.Context, req *connect.Request[zarlv1.ReviewSensorProposalRequest]) (*connect.Response[zarlv1.ReviewSensorProposalResponse], error) {
	status := "rejected"
	if req.Msg.Approve {
		status = "approved"
	}
	if err := a.sensorProposals.SetStatus(ctx, req.Msg.Id, status); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Approved sensors load at startup. A hot-register path would avoid a
	// restart — follow-up; for now we notify the user.
	if req.Msg.Approve && a.notifications != nil {
		a.notifications.Broadcast(znotify.Notification{
			ToolName: "self_improvement",
			Content:  "Sensor proposal approved. Restart zarl to activate it.",
		})
	}
	return connect.NewResponse(&zarlv1.ReviewSensorProposalResponse{Status: status}), nil
}
