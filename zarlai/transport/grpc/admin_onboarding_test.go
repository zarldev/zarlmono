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

func TestCompleteOnboarding_Integration(t *testing.T) {
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
	settings := repository.NewSettingsRepo(q)
	qc := qdrant.NewClient("http://localhost:6333")

	if _, err := persons.DeleteAll(ctx); err != nil {
		t.Logf("pre-test cleanup: %v", err)
	}

	server := transportgrpc.NewAdminServer(transportgrpc.AdminConfig{
		Persons:  persons,
		Settings: settings,
		Qdrant:   qc,
	})

	embs := []*zarlv1.FaceEmbedding{
		{Values: makeOnb128Embedding(0.1)},
		{Values: makeOnb128Embedding(0.7)},
		{Values: makeOnb128Embedding(1.3)},
	}
	resp, err := server.CompleteOnboarding(ctx, connect.NewRequest(&zarlv1.CompleteOnboardingRequest{
		AgentName:      "Zarl",
		VoiceSpeaker:   8,
		VoiceSpeed:     1.1,
		LlmModel:       "qwen3.6-35b-a3b",
		PersonName:     "TestUser",
		PersonPronouns: "they/them",
		FaceEmbeddings: embs,
	}))
	if err != nil {
		t.Fatalf("complete onboarding: %v", err)
	}
	if resp.Msg.PersonId == "" {
		t.Fatal("expected person_id")
	}

	got, err := persons.GetByName(ctx, "TestUser")
	if err != nil {
		t.Fatalf("get TestUser: %v", err)
	}
	if len(got.Embeddings) != 3 {
		t.Errorf("want 3 stored embeddings, got %d", len(got.Embeddings))
	}

	if v, err := settings.Get(ctx, "agent_name"); err != nil || v != "Zarl" {
		t.Errorf("agent_name: got %q err=%v", v, err)
	}
	if v, err := settings.Get(ctx, "llm_model"); err != nil || v != "qwen3.6-35b-a3b" {
		t.Errorf("llm_model: got %q err=%v", v, err)
	}
	if v, err := settings.Get(ctx, "voice"); err != nil || v != "8:1.10" {
		t.Errorf("voice: got %q err=%v", v, err)
	}
}

func makeOnb128Embedding(seed float32) []float32 {
	out := make([]float32, 128)
	for i := range out {
		out[i] = seed + float32(i)/1000
	}
	return out
}
