package grpc_test

import (
	"context"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
	transportgrpc "github.com/zarldev/zarlmono/zarlai/transport/grpc"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// requires docker compose stack (dolt :3307, qdrant :6333) up.
func TestResetAgentState_Integration(t *testing.T) {
	if os.Getenv("ZARL_INTEGRATION") == "" {
		t.Skip("set ZARL_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := repository.NewDB("root:@tcp(localhost:3307)/zarl?parseTime=true")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	q := gen.New(db)
	persons := repository.NewPersonRepo(q)
	summaries := repository.NewConversationSummaryRepo(q)
	toolCalls := repository.NewToolCallRepo(q)
	qc := qdrant.NewClient("http://localhost:6333")

	// Seed: one person.
	if _, err := persons.Create(ctx, "TestPerson", [][]float32{makeReset128Embedding(0.5)}, ""); err != nil {
		t.Fatalf("seed person: %v", err)
	}

	server := transportgrpc.NewAdminServer(transportgrpc.AdminConfig{
		Persons:   persons,
		Summaries: summaries,
		ToolCalls: toolCalls,
		Qdrant:    qc,
	})

	resp, err := server.ResetAgentState(ctx, connect.NewRequest(&zarlv1.ResetAgentStateRequest{}))
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if resp.Msg.PersonsDeleted < 1 {
		t.Errorf("want at least 1 person deleted, got %d", resp.Msg.PersonsDeleted)
	}

	left, err := persons.List(ctx)
	if err != nil {
		t.Fatalf("list after wipe: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("want 0 persons after reset, got %d", len(left))
	}
}

func makeReset128Embedding(seed float32) []float32 {
	out := make([]float32, 128)
	for i := range out {
		out[i] = seed + float32(i)/1000
	}
	return out
}
