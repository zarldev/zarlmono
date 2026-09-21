// Binary deterministic_trace writes runner and workflow telemetry to a
// deterministic JSONL artifact, then reads the artifact back without an LLM or
// network access.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/workflow"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const taskID taskscope.ID = "trace-example"

type event struct {
	Sequence int          `json:"sequence"`
	Source   string       `json:"source"`
	Kind     string       `json:"kind"`
	TaskID   taskscope.ID `json:"task_id,omitempty"`
	Name     string       `json:"name,omitempty"`
	// Fields holds the typed payload selected by Kind; events without data omit it.
	Fields any `json:"fields,omitempty"`
}

type conversationStartedFields struct {
	Prompt string `json:"prompt"`
}

type toolStartedFields struct {
	ToolID string `json:"tool_id"`
}

type toolCompletedFields struct {
	Result string `json:"result"`
	ToolID string `json:"tool_id"`
}

type iterationFields struct {
	Iteration int `json:"iteration"`
}

type contentFields struct {
	Delta string `json:"delta"`
}

type conversationEndedFields struct {
	Iterations int                   `json:"iterations"`
	Reason     runner.TerminalReason `json:"reason"`
}

type failureFields struct {
	Error string `json:"error"`
}

type jsonlExporter struct {
	mu   sync.Mutex
	w    io.Writer
	next int
}

func (e *jsonlExporter) export(v event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.next++
	v.Sequence = e.next
	if err := json.NewEncoder(e.w).Encode(v); err != nil {
		return fmt.Errorf("write JSONL event: %w", err)
	}
	return nil
}

type telemetrySink struct {
	runner.NopSink
	exporter *jsonlExporter
	mu       sync.Mutex
	err      error
}

func (s *telemetrySink) emit(v event) {
	if err := s.exporter.export(v); err != nil {
		s.mu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
	}
}

func (s *telemetrySink) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *telemetrySink) OnConversationStarted(ctx context.Context, e runner.ConversationStarted) {
	s.emit(event{Source: "runner", Kind: "conversation_started", TaskID: e.TaskID, Fields: conversationStartedFields{Prompt: e.Prompt}})
}

func (s *telemetrySink) OnToolStarted(ctx context.Context, e runner.ToolStarted) {
	s.emit(event{Source: "runner", Kind: "tool_started", TaskID: e.TaskID, Name: e.ToolName, Fields: toolStartedFields{ToolID: e.ToolID}})
}

func (s *telemetrySink) OnToolCompleted(ctx context.Context, e runner.ToolCompleted) {
	s.emit(event{Source: "runner", Kind: "tool_completed", TaskID: e.TaskID, Name: e.ToolName, Fields: toolCompletedFields{Result: e.FormattedResult, ToolID: e.ToolID}})
}

func (s *telemetrySink) OnIterationCompleted(ctx context.Context, e runner.IterationCompleted) {
	s.emit(event{Source: "runner", Kind: "iteration_completed", TaskID: e.TaskID, Fields: iterationFields{Iteration: e.Iter}})
}

func (s *telemetrySink) OnContent(ctx context.Context, e runner.Content) {
	s.emit(event{Source: "runner", Kind: "content", TaskID: e.TaskID, Fields: contentFields{Delta: e.Delta}})
}

func (s *telemetrySink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	s.emit(event{Source: "runner", Kind: "conversation_ended", TaskID: e.TaskID, Fields: conversationEndedFields{Iterations: e.Iterations, Reason: e.Reason}})
}

func (s *telemetrySink) OnWorkflowStarted(workflow.Started) {
	s.emit(event{Source: "workflow", Kind: "workflow_started"})
}

func (s *telemetrySink) OnWorkflowNodeStarted(e workflow.NodeStarted) {
	s.emit(event{Source: "workflow", Kind: "workflow_node_started", Name: e.Node.String()})
}

func (s *telemetrySink) OnWorkflowNodeCompleted(e workflow.NodeCompleted) {
	s.emit(event{Source: "workflow", Kind: "workflow_node_completed", Name: e.Node.String()})
}

func (s *telemetrySink) OnWorkflowNodeFailed(e workflow.NodeFailed) {
	s.emit(event{Source: "workflow", Kind: "workflow_node_failed", Name: e.Node.String(), Fields: failureFields{Error: e.Error.Error()}})
}

func (s *telemetrySink) OnWorkflowCompleted(workflow.Completed) {
	s.emit(event{Source: "workflow", Kind: "workflow_completed"})
}

func (s *telemetrySink) OnWorkflowFailed(e workflow.Failed) {
	s.emit(event{Source: "workflow", Kind: "workflow_failed", Fields: failureFields{Error: e.Error.Error()}})
}

func main() {
	out := flag.String("out", "trace.jsonl", "JSONL artifact path")
	flag.Parse()
	if err := run(context.Background(), *out, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, stdout io.Writer) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create trace artifact: %w", err)
	}
	sink := &telemetrySink{exporter: &jsonlExporter{w: file}}

	if err := runAgent(ctx, sink); err != nil {
		_ = file.Close()
		return err
	}
	if err := runWorkflow(ctx, sink); err != nil {
		_ = file.Close()
		return err
	}
	if err := sink.Err(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close trace artifact: %w", err)
	}

	events, err := readEvents(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "artifact=%s events=%d\n", path, len(events))
	for _, e := range events {
		fmt.Fprintf(stdout, "%02d %s.%s", e.Sequence, e.Source, e.Kind)
		if e.Name != "" {
			fmt.Fprintf(stdout, " name=%s", e.Name)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

type lookupArgs struct {
	Key string `json:"key" doc:"Key to look up in the fixture"`
}

func runAgent(ctx context.Context, sink runner.EventSink) error {
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("lookup-1", "lookup", `{"key":"answer"}`)},
		{runnertest.ChunkText("The deterministic answer is 42.")},
	})
	lookup := tools.New(tools.ToolSpec{
		Name:        "lookup",
		Description: "Return a canned value.",
		Parameters:  tools.SchemaFor[lookupArgs](),
	}, func(_ context.Context, _ lookupArgs) (string, error) {
		return "42", nil
	})
	registry := tools.NewRegistry(lookup)
	r := runner.New(client, runner.WithTools(registry), runner.WithSink(sink), runner.WithMaxIterations(3))
	result := r.Run(ctx, runner.TaskSpec{ID: taskID, Prompt: "Find the deterministic answer."})
	if result.Err != nil {
		return fmt.Errorf("run scripted agent: %w", result.Err)
	}
	return nil
}

func runWorkflow(ctx context.Context, sink workflow.EventSink) error {
	graph := workflow.NewGraph()
	if err := workflow.AddNode(graph, "double", workflow.NodeFunc[int, int](func(_ context.Context, n int) (int, error) { return n * 2, nil })); err != nil {
		return err
	}
	if err := graph.AddEdge(workflow.Start.String(), "double"); err != nil {
		return err
	}
	if err := graph.AddEdge("double", workflow.End.String()); err != nil {
		return err
	}
	runnable, err := graph.Compile()
	if err != nil {
		return err
	}
	runnable.Sink = sink
	if _, err := runnable.Invoke(ctx, 21); err != nil {
		return fmt.Errorf("run workflow: %w", err)
	}
	return nil
}

func readEvents(path string) ([]event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace artifact: %w", err)
	}
	defer file.Close()

	var events []event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var e event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("decode trace event: %w", err)
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read trace artifact: %w", err)
	}
	return events, nil
}
