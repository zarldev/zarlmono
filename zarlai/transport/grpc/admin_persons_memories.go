package grpc

import (
	"context"
	"fmt"
	"sort"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

func (a *AdminServer) ListPersonMemories(ctx context.Context, req *connect.Request[zarlv1.ListPersonMemoriesRequest]) (*connect.Response[zarlv1.ListPersonMemoriesResponse], error) {
	if req.Msg.PersonName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("person_name is required"))
	}

	filter := &qdrant.Filter{
		Must: []qdrant.FieldCondition{
			{Key: "person_name", Match: qdrant.MatchValue{Value: req.Msg.PersonName}},
		},
	}

	points, _, err := a.qdrantClient.Scroll(ctx, qdrant.ScrollRequest{
		Collection: memory.Collection,
		Filter:     filter,
		Limit:      200,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scroll memories: %w", err))
	}

	msgs := make([]*zarlv1.PersonMemoryMsg, 0, len(points))
	for _, p := range points {
		fact, _ := p.Payload["fact"].(string)
		createdAt, _ := p.Payload["created_at"].(string)
		msgs = append(msgs, &zarlv1.PersonMemoryMsg{
			Id:        p.ID,
			Fact:      fact,
			CreatedAt: createdAt,
		})
	}

	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].CreatedAt > msgs[j].CreatedAt
	})

	return connect.NewResponse(&zarlv1.ListPersonMemoriesResponse{Memories: msgs}), nil
}

func (a *AdminServer) DeletePersonMemory(ctx context.Context, req *connect.Request[zarlv1.DeletePersonMemoryRequest]) (*connect.Response[zarlv1.DeletePersonMemoryResponse], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id is required"))
	}

	if err := a.qdrantClient.DeleteByID(ctx, memory.Collection, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete memory: %w", err))
	}

	return connect.NewResponse(&zarlv1.DeletePersonMemoryResponse{}), nil
}
