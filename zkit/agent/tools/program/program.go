// Package program exposes a provider-neutral programmatic tool that runs a
// bounded Starlark script over an allowlisted inner tool source.
package program

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
	"go.starlark.net/starlark"
)

// ToolName is the LLM-visible programmatic tool name.
const ToolName tools.ToolName = "program"

const op = "program"

// Args are the program tool arguments.
type Args struct {
	Script string `json:"script" doc:"Starlark script to run. Use only call(name,args), call_many(calls), and emit(value). Must call emit with the final JSON-shaped answer."`
}

// Result is the structured successful output returned by the program tool.
type Result struct {
	Output any   `json:"output"`
	Stats  Stats `json:"stats"`
}

// String renders only the script's emitted output for model/TUI text paths.
// Execution stats remain available through structured Data and metadata, but
// dumping the wrapper as JSON makes the transcript noisy.
func (r Result) String() string {
	if s, ok := r.Output.(string); ok {
		return s
	}
	b, err := json.Marshal(r.Output)
	if err != nil {
		return fmt.Sprint(r.Output)
	}
	return string(b)
}

// Stats describes bounded interpreter work performed by a program call.
type Stats struct {
	ToolCalls       int           `json:"tool_calls"`
	ParallelBatches int           `json:"parallel_batches"`
	ExecutionSteps  uint64        `json:"execution_steps"`
	Duration        time.Duration `json:"duration"`
}

// Limits bounds one program invocation.
type Limits struct {
	MaxScriptBytes     int
	MaxExecutionSteps  uint64
	MaxToolCalls       int
	MaxParallelCalls   int
	MaxToolResultBytes int
	MaxOutputBytes     int
	Timeout            time.Duration
}

// Policy decides whether a currently visible inner tool may be called from a
// program script.
type Policy func(tools.ToolSpec) bool

// Source wraps an inner source with the synthetic program tool.
type Source struct {
	inner  tools.Source
	policy Policy
	limits Limits
}

// NewSource returns a source that exposes program alongside inner's tools.
func NewSource(inner tools.Source, opts ...options.Option[Source]) (*Source, error) {
	if inner == nil {
		return nil, errors.New("program source: inner source is nil")
	}
	s := &Source{inner: inner, policy: denyAllPolicy, limits: defaultLimits()}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.policy == nil {
		s.policy = denyAllPolicy
	}
	limits, err := normalizeLimits(s.limits)
	if err != nil {
		return nil, err
	}
	s.limits = limits
	return s, nil
}

// WithPolicy sets the nested tool policy. A nil policy restores deny-all.
func WithPolicy(policy Policy) options.Option[Source] {
	return func(s *Source) {
		if policy == nil {
			s.policy = denyAllPolicy
			return
		}
		s.policy = policy
	}
}

// WithLimits sets invocation limits. Zero fields receive documented defaults.
func WithLimits(limits Limits) options.Option[Source] {
	return func(s *Source) { s.limits = limits }
}

// Tools yields inner tools and then the synthetic program tool unless inner
// already exposes that name.
func (s *Source) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		hasProgram := false
		for tool := range s.inner.Tools(ctx) {
			spec := tool.Definition()
			if spec.Name == ToolName {
				hasProgram = true
				continue
			}
			if s.policy(spec) {
				continue
			}
			if !yield(tool) {
				return
			}
		}
		if hasProgram {
			return
		}
		_ = yield(programTool{source: s})
	}
}

// Execute runs program calls and delegates all other calls to the inner source.
func (s *Source) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if call.ToolName != ToolName {
		return s.inner.Execute(ctx, call)
	}
	return programTool{source: s}.Execute(ctx, call)
}

type programTool struct{ source *Source }

const programDescription = `Use this for bounded read/search/catalogue fan-out when programmatic_tools is enabled. The direct read/search/catalogue tools are hidden. Use this program tool for reading, listing, grepping, code retrieval, web search/fetch, and catalogue listing. Use bash only for real shell work such as git, builds, tests, package managers, or generated-code commands.

Write a short Starlark script (Python-like syntax). This is not shell, and there is no generic "list" command. The only built-ins are:
- call(name, args={}) -> {"ok": bool, "data": value, "error": string}
- call_many([{"name": name, "args": args}, ...]) -> results in input order
- emit(value) -> final JSON-shaped result; every script must call emit

Allowed tool names and common args:
- read: {"path": string, "offset": int, "limit": int}
- grep: {"pattern": string, "path": string, "glob": string, "case_insensitive": bool, "max_results": int, "output": "labeled"|"json"}
- glob: {"pattern": string, "root": string, "include_dirs": bool, "max_results": int, "output": "labeled"|"json"}
- ls: {"path": string, "show_hidden": bool, "output": "labeled"|"json"}
- file_map: {"root": string, "pattern": string, "include_tests": bool, "max_files": int, "output": "labeled"|"json"}
- retrieve_code: {"query": string, "root": string, "pattern": string, "include_tests": bool, "limit": int, "max_files": int, "max_bytes_per_chunk": int, "output": "labeled"|"json"}
- web_search: {"query": string, "max_results": int, "output": "labeled"|"json"}
- web_fetch: {"url": string, "max_chars": int, "selector": string, "use_browser": bool}
- list_skills/list_agents/list_instructions: {}

Good pattern:
results = call_many([
  {"name": "grep", "args": {"pattern": "TODO", "path": "zkit", "glob": "*.go", "output": "json"}},
  {"name": "glob", "args": {"pattern": "**/*_test.go", "root": "zkit", "output": "json"}},
])
emit([r for r in results if r["ok"]])

Limits: read-only allowlist only; no imports, filesystem, network, shell, environment, process access, mutation, MCP, spawn_agent, or recursive program calls. Use direct edit/write tools for changes and bash for real shell commands.`

func (t programTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolName,
		Description: programDescription,
		Parameters:  tools.SchemaFor[Args]()}
}

func (t programTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	started := time.Now()
	metadata := tools.NewToolMetadata(ToolName)

	args, decodeErr := tools.DecodeArgs[Args](call.Arguments)
	if decodeErr != nil {
		errObj := tools.Validation(op, decodeErr.Error())
		return failure(call.ID, errObj, metadata.SetError(errObj).SetExecutionTime(time.Since(started))), nil
	}

	runner := newRunner(ctx, t.source, call.ID, started)
	output, stats, runErr := runner.run(args.Script)
	metadata["nested_events"] = true
	metadata["tool_calls"] = stats.ToolCalls
	metadata["parallel_batches"] = stats.ParallelBatches
	metadata["execution_steps"] = stats.ExecutionSteps
	metadata.SetExecutionTime(stats.Duration)
	if runErr != nil {
		metadata.SetError(runErr)
		return failure(call.ID, runErr, metadata), nil
	}
	return &tools.ToolResult{
		ToolCallID: call.ID,
		Success:    true,
		Data:       Result{Output: output, Stats: stats},
		Metadata:   metadata,
		ExecutedAt: time.Now(),
	}, nil
}

func failure(id tools.ToolCallID, errObj *tools.Error, metadata tools.ToolMetadata) *tools.ToolResult {
	return &tools.ToolResult{ToolCallID: id, Success: false, Error: errObj.Error(), Err: errObj, Metadata: metadata, ExecutedAt: time.Now()}
}

func denyAllPolicy(tools.ToolSpec) bool { return false }

func (s *Source) findAllowed(ctx context.Context, name tools.ToolName) *tools.Error {
	if name == ToolName {
		return tools.Permission(op, "program cannot call itself")
	}
	for tool := range s.inner.Tools(ctx) {
		spec := tool.Definition()
		if spec.Name != name {
			continue
		}
		if !s.policy(spec) {
			return tools.Permission(op, fmt.Sprintf("tool %q is not allowed from program", name))
		}
		return nil
	}
	return tools.NotFound(op, fmt.Sprintf("tool %q is not visible", name))
}

func contextError(err error) *tools.Error {
	if errors.Is(err, context.Canceled) {
		return tools.Transient(op, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return tools.Budget(op, context.DeadlineExceeded.Error())
	}
	return tools.Fatal(op, err)
}

func newBuiltin(name string, fn func(*starlark.Thread, starlark.Tuple, []starlark.Tuple) (starlark.Value, error)) *starlark.Builtin {
	return starlark.NewBuiltin(name, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return fn(thread, args, kwargs)
	})
}
