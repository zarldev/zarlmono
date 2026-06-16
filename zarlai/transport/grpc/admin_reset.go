package grpc

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// taskFindingsCollection is the Qdrant collection the taskrunner writes
// findings into. Keeping the literal here (rather than a taskrunner export)
// avoids a transport→taskrunner package dependency just for the name.
const taskFindingsCollection = "task_findings"

// ResetAgentState wipes the polluted-data subsystems but keeps curated
// configuration (skills, tools, prompts, settings, providers, profiles,
// proposals). Best-effort per subsystem — a subsystem failure is logged and
// the rest still proceed; counts in the response reflect what actually got
// wiped.
func (a *AdminServer) ResetAgentState(ctx context.Context, req *connect.Request[zarlv1.ResetAgentStateRequest]) (*connect.Response[zarlv1.ResetAgentStateResponse], error) {
	resp := &zarlv1.ResetAgentStateResponse{}

	if a.persons != nil {
		n, err := a.persons.DeleteAll(ctx)
		if err != nil {
			slog.Warn("reset: persons delete failed", "err", err)
		}
		resp.PersonsDeleted = n
	}

	if a.summaries != nil {
		n, err := a.summaries.DeleteAll(ctx)
		if err != nil {
			slog.Warn("reset: conversation summaries delete failed", "err", err)
		}
		resp.ConversationSummariesDeleted = n
	}

	if a.toolCalls != nil {
		n, err := a.toolCalls.DeleteAll(ctx)
		if err != nil {
			slog.Warn("reset: tool calls delete failed", "err", err)
		}
		resp.ToolCallsDeleted = n
	}

	if a.qdrantClient != nil {
		if n, err := wipeQdrantCollection(ctx, a, memory.Collection); err != nil {
			slog.Warn("reset: memories collection wipe failed", "err", err)
		} else {
			resp.MemoriesDeleted = n
		}
		if n, err := wipeQdrantCollection(ctx, a, taskFindingsCollection); err != nil {
			slog.Warn("reset: task findings collection wipe failed", "err", err)
		} else {
			resp.TaskFindingsDeleted = n
		}
	}

	a.emitConfigChange(fmt.Sprintf(
		"Agent state reset: %d persons, %d summaries, %d tool calls, %d memories, %d task findings.",
		resp.PersonsDeleted, resp.ConversationSummariesDeleted, resp.ToolCallsDeleted,
		resp.MemoriesDeleted, resp.TaskFindingsDeleted,
	))
	return connect.NewResponse(resp), nil
}

// wipeQdrantCollection scrolls a collection counting current points, then
// deletes them all by ID. Returns the count that existed before the wipe.
// If the collection doesn't exist or is empty, returns 0.
func wipeQdrantCollection(ctx context.Context, a *AdminServer, collection string) (int64, error) {
	var total int64
	var offset any
	for {
		points, next, err := a.qdrantClient.Scroll(ctx, qdrant.ScrollRequest{Collection: collection, Limit: 1024, Offset: offset})
		if err != nil {
			return 0, fmt.Errorf("scroll %s: %w", collection, err)
		}
		total += int64(len(points))
		if next == nil {
			break
		}
		offset = next
	}

	if total == 0 {
		return 0, nil
	}
	offset = nil
	for {
		points, next, err := a.qdrantClient.Scroll(ctx, qdrant.ScrollRequest{Collection: collection, Limit: 1024, Offset: offset})
		if err != nil {
			return total, fmt.Errorf("scroll %s for delete: %w", collection, err)
		}
		for _, p := range points {
			if err := a.qdrantClient.DeleteByID(ctx, collection, p.ID); err != nil {
				slog.Warn("reset: delete point failed", "collection", collection, "id", p.ID, "err", err)
			}
		}
		if next == nil {
			break
		}
		offset = next
	}
	return total, nil
}
