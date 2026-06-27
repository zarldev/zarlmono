// Binary shared_infra demonstrates retrieval, workflow, checkpointing, HITL,
// and tracing without an LLM or external service.
package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/checkpoint"
	"github.com/zarldev/zarlmono/zkit/agent/hitl"
	agentretrieval "github.com/zarldev/zarlmono/zkit/agent/retrieval"
	"github.com/zarldev/zarlmono/zkit/agent/trace"
	"github.com/zarldev/zarlmono/zkit/agent/workflow"
	airetrieval "github.com/zarldev/zarlmono/zkit/ai/retrieval"
)

// Request is the workflow input.
type Request struct {
	Query string
}

// Draft is produced by retrieval and later reviewed.
type Draft struct {
	Query      string
	Context    string
	Checkpoint checkpoint.ID
	Review     hitl.Review
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdout io.Writer) error {
	store := airetrieval.NewMemoryVectorStore()
	embedder := hashEmbedder{}
	pipe := airetrieval.Pipeline{
		Chunker:  airetrieval.TextChunker{Size: 160, Overlap: 20},
		Embedder: embedder,
		Store:    store,
	}
	if err := pipe.Index(ctx, corpus()); err != nil {
		return err
	}

	retriever := airetrieval.VectorRetriever{Embedder: embedder, Store: store, Limit: 2}
	checkpoints := checkpoint.NewMemoryStore()

	graph := workflow.NewGraph()
	if err := workflow.AddNode(graph, "retrieve", workflow.NodeFunc[Request, Draft](func(ctx context.Context, req Request) (Draft, error) {
		docs, err := retriever.Retrieve(ctx, req.Query, airetrieval.WithLimit(2))
		if err != nil {
			return Draft{}, err
		}
		return Draft{Query: req.Query, Context: agentretrieval.FormatDocuments(docs, agentretrieval.FormatOptions{Title: "Context", MaxDocs: 2, MaxRunes: 180, ShowScores: true})}, nil
	})); err != nil {
		return err
	}
	if err := workflow.AddNode(graph, "checkpoint", workflow.NodeFunc[Draft, Draft](func(ctx context.Context, draft Draft) (Draft, error) {
		draft.Checkpoint = "draft-1"
		return draft, checkpoints.Save(ctx, checkpoint.Checkpoint{ID: draft.Checkpoint, RunID: "shared-infra", Step: "before-review", State: map[string]any{"query": draft.Query, "context": draft.Context}})
	})); err != nil {
		return err
	}
	if err := workflow.AddNode(graph, "review", workflow.NodeFunc[Draft, Draft](func(ctx context.Context, draft Draft) (Draft, error) {
		req := hitl.Request{ID: "review-1", RunID: "shared-infra", CheckpointID: string(draft.Checkpoint), Action: "publish_answer", Summary: "Answer using retrieved context", Risk: hitl.RiskMedium}
		review, decided, err := hitl.ApproveLowRisk{}.Review(ctx, req)
		if err != nil {
			return Draft{}, err
		}
		if !decided {
			review = hitl.Review{RequestID: req.ID, Decision: hitl.DecisionApprove, Reviewer: "example-human", Comment: "Context is relevant; proceed."}
		}
		draft.Review = review
		return draft, nil
	})); err != nil {
		return err
	}

	_ = graph.AddEdge(workflow.Start, "retrieve")
	_ = graph.AddEdge("retrieve", "checkpoint")
	_ = graph.AddEdge("checkpoint", "review")
	_ = graph.AddEdge("review", workflow.End)

	runnable, err := graph.Compile()
	if err != nil {
		return err
	}
	runnable.Sink = &trace.WorkflowSink{Exporter: trace.NewJSONLExporter(stdout)}

	out, _, err := runnable.InvokeState(ctx, Request{Query: "How do agents resume after human approval?"})
	if err != nil {
		return err
	}
	draft, ok := out.(Draft)
	if !ok {
		return fmt.Errorf("unexpected workflow output type %T", out)
	}
	fmt.Fprintf(stdout, "\nanswer_source=%s checkpoint=%s review=%s reviewer=%s\n%s\n", draft.Query, draft.Checkpoint, draft.Review.Decision, draft.Review.Reviewer, draft.Context)
	return nil
}

func corpus() []airetrieval.Document {
	return []airetrieval.Document{
		{ID: "retrieval", Text: "Retrieval turns external documents into agent context. Core RAG primitives live in ai/retrieval; agent/retrieval formats retrieved documents as prompt context or a tool result.", Metadata: airetrieval.Metadata{"topic": "retrieval"}},
		{ID: "hitl", Text: "Checkpointing stores resumable run state before a risky step. Human-in-the-loop review records an approval, denial, or edited decision and can resume from the checkpoint.", Metadata: airetrieval.Metadata{"topic": "hitl"}},
		{ID: "workflow", Text: "Workflows compose typed nodes with static or conditional edges. Workflow events can be exported through trace sinks such as JSONL.", Metadata: airetrieval.Metadata{"topic": "workflow"}},
	}
}

type hashEmbedder struct{}

func (hashEmbedder) Embed(_ context.Context, texts []string) ([]airetrieval.Vector, error) {
	vectors := make([]airetrieval.Vector, 0, len(texts))
	for _, text := range texts {
		vectors = append(vectors, embed(text))
	}
	return vectors, nil
}

func embed(text string) airetrieval.Vector {
	vec := make(airetrieval.Vector, 8)
	for _, word := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		vec[int(h.Sum32())%len(vec)]++
	}
	return vec
}
