package tools

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ToolName is a typed identifier for a tool.
type ToolName string

// ToolCallID identifies one model-requested tool invocation.
type ToolCallID string

// String returns the string representation.
func (id ToolCallID) String() string { return string(id) }

// String returns the string representation.
func (n ToolName) String() string { return string(n) }

// Iterable is the read side of a tool source: a cheap, read-only snapshot of
// the tools currently visible to a single runner iteration. Implementations
// must not perform I/O, block on work, mutate state, or hold a lock that an
// in-flight Execute call could need. The ctx carries request-scoped values
// such as task depth; use it only to decide visibility, not to trigger work.
type Iterable interface {
	Tools(ctx context.Context) iter.Seq[Tool]
}

// Executor dispatches a tool call. Implementations translate the call's name
// and arguments into a concrete tool invocation; transport details are theirs
// to own.
type Executor interface {
	Execute(ctx context.Context, c ToolCall) (*ToolResult, error)
}

// Source is the canonical live tool source contract: enumerate visible tools
// and execute calls against them.
type Source interface {
	Iterable
	Executor
}

// Common tool name constants used by default registries and examples.
const (
	ToolNameWebSearch ToolName = "web_search"
	ToolNameWebFetch  ToolName = "web_fetch"
)

// ToolParameters are raw model-provided arguments at the tool dispatch
// boundary. Prefer DecodeArgs or NewTyped in tool implementations so business
// logic receives a typed argument struct.
type ToolParameters map[string]any

// String returns the value at key as a string, or defaultValue if missing.
func (tp ToolParameters) String(key, defaultValue string) string {
	if val, ok := tp[key].(string); ok {
		return val
	}
	return defaultValue
}

// Int returns the value at key as an int, or defaultValue if missing or
// unconvertible.
func (tp ToolParameters) Int(key string, defaultValue int) int {
	switch val := tp[key].(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultValue
}

// Bool returns the value at key as a bool, or defaultValue if missing.
func (tp ToolParameters) Bool(key string, defaultValue bool) bool {
	if val, ok := tp[key].(bool); ok {
		return val
	}
	return defaultValue
}

// Float returns the value at key as a float64, or defaultValue if
// missing or unconvertible. JSON numbers always decode as float64
// through encoding/json so the type assertion catches the common case.
func (tp ToolParameters) Float(key string, defaultValue float64) float64 {
	switch val := tp[key].(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

// Slice returns the value at key as a []string. Each element is
// stringified — strings pass through, anything else uses
// fmt.Sprint. Missing or wrong-shape entries return nil so callers
// can treat them the same as the zero value.
func (tp ToolParameters) Slice(key string) []string {
	if raw, ok := tp[key].([]any); ok {
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out = append(out, s)
				continue
			}
			out = append(out, fmt.Sprint(v))
		}
		return out
	}
	// Already-typed slice? Unusual via JSON but support it for callers
	// that build ToolParameters by hand.
	if direct, ok := tp[key].([]string); ok {
		return direct
	}
	return nil
}

// Map returns the value at key as a map[string]string. Values are
// stringified — strings pass through, anything else uses fmt.Sprint.
// Missing or wrong-shape entries return nil.
func (tp ToolParameters) Map(key string) map[string]string {
	if raw, ok := tp[key].(map[string]any); ok {
		out := make(map[string]string, len(raw))
		for k, v := range raw {
			if s, ok := v.(string); ok {
				out[k] = s
				continue
			}
			out[k] = fmt.Sprint(v)
		}
		return out
	}
	if direct, ok := tp[key].(map[string]string); ok {
		return direct
	}
	return nil
}

// ToolMetadata carries result metadata (timing, error info, cache state).
type ToolMetadata map[string]any

// NewToolMetadata creates ToolMetadata pre-populated with the tool name.
func NewToolMetadata(toolName ToolName) ToolMetadata {
	metadata := make(ToolMetadata)
	metadata["tool_name"] = toolName.String()
	return metadata
}

// SetExecutionTime records execution duration in the metadata.
func (tm ToolMetadata) SetExecutionTime(duration time.Duration) ToolMetadata {
	tm["execution_time"] = duration.String()
	tm["execution_time_ms"] = duration.Milliseconds()
	return tm
}

// SetError records an error in the metadata.
func (tm ToolMetadata) SetError(err error) ToolMetadata {
	if err != nil {
		tm["error"] = err.Error()
		tm["has_error"] = true
	}
	return tm
}

// SetCacheHit records cache hit status.
func (tm ToolMetadata) SetCacheHit(hit bool) ToolMetadata {
	tm["cache_hit"] = hit
	return tm
}

// SetToolInfo records the tool name and any expression context.
func (tm ToolMetadata) SetToolInfo(toolName ToolName, expression string) ToolMetadata {
	tm["tool_name"] = toolName.String()
	if expression != "" {
		tm["expression"] = expression
	}
	return tm
}

// EffectKind classifies a post-action effect emitted by a tool. Effects
// are control-plane facts for guardrails, harnesses, audit views, and eval
// reporting; they are not automatically rendered into the LLM transcript.
type EffectKind string

const (
	// EffectFile records a filesystem effect inside the tool workspace.
	EffectFile EffectKind = "file"
	// EffectProcess records a process effect, usually from the bash tool.
	EffectProcess EffectKind = "process"
)

// FileOp is the concrete filesystem operation a file effect represents.
type FileOp string

// The recognised file operations. Rename is the only one that populates
// FileEffect.FromPath alongside Path.
const (
	FileRead   FileOp = "read"
	FileCreate FileOp = "create"
	FileModify FileOp = "modify"
	FileAppend FileOp = "append"
	FileDelete FileOp = "delete"
	FileRename FileOp = "rename"
)

// Effect is one post-action fact produced by a tool. The Kind field
// selects which optional payload is populated. Pointer payloads are used
// because nil is meaningful: a file effect has no process payload, and a
// process effect has no file payload.
type Effect struct {
	Kind    EffectKind     `json:"kind"`
	File    *FileEffect    `json:"file,omitempty"`
	Process *ProcessEffect `json:"process,omitempty"`
}

// FileEffect describes a filesystem action. Paths are workspace-relative.
type FileEffect struct {
	Path       string `json:"path,omitempty"`
	FromPath   string `json:"from_path,omitempty"`
	Op         FileOp `json:"op,omitempty"`
	BytesAfter int64  `json:"bytes_after,omitempty"`
}

// ProcessEffect describes a process launched by a tool. File effects caused
// by that process are intentionally out of scope for the first pass; callers
// can derive those separately with snapshot/diff wrappers when needed.
type ProcessEffect struct {
	Command          string `json:"command,omitempty"`
	ExitCode         int    `json:"exit_code,omitempty"`
	Background       bool   `json:"background,omitempty"`
	ProcessID        string `json:"process_id,omitempty"`
	PID              int    `json:"pid,omitempty"`
	TimedOut         bool   `json:"timed_out,omitempty"`
	OutputTruncated  bool   `json:"output_truncated,omitempty"`
	AutoBackgrounded bool   `json:"auto_backgrounded,omitempty"`
}

// NewFileEffect returns a file effect for op/path.
func NewFileEffect(op FileOp, path string) Effect {
	return Effect{Kind: EffectFile, File: &FileEffect{Path: path, Op: op}}
}

// NewProcessEffect returns a process effect for command/exitCode.
func NewProcessEffect(command string, exitCode int) Effect {
	return Effect{Kind: EffectProcess, Process: &ProcessEffect{Command: command, ExitCode: exitCode}}
}

// Validate reports whether e has exactly the payload selected by Kind.
func (e Effect) Validate() error {
	switch e.Kind {
	case EffectFile:
		if e.File == nil || e.Process != nil {
			return fmt.Errorf("effect %q requires file payload only", e.Kind)
		}
	case EffectProcess:
		if e.Process == nil || e.File != nil {
			return fmt.Errorf("effect %q requires process payload only", e.Kind)
		}
	default:
		return fmt.Errorf("effect kind %q is invalid", e.Kind)
	}
	return nil
}

// IsFile reports whether e is a valid file effect.
func (e Effect) IsFile() bool { return e.Validate() == nil && e.Kind == EffectFile }

// IsProcess reports whether e is a valid process effect.
func (e Effect) IsProcess() bool { return e.Validate() == nil && e.Kind == EffectProcess }

// ToolCallStatus is the lifecycle state of a tool call.
type ToolCallStatus string

// The tool-call lifecycle states, in order: a call is created Pending,
// marked Executing at dispatch, and ends Completed or Failed.
const (
	ToolCallStatusPending   ToolCallStatus = "pending"
	ToolCallStatusExecuting ToolCallStatus = "executing"
	ToolCallStatusCompleted ToolCallStatus = "completed"
	ToolCallStatusFailed    ToolCallStatus = "failed"
)

// String returns the string representation.
func (s ToolCallStatus) String() string { return string(s) }

// ToolCall is a structured tool invocation.
type ToolCall struct {
	ID        ToolCallID     `json:"id"`
	ToolName  ToolName       `json:"tool_name"`
	Arguments ToolParameters `json:"arguments"`
	Status    ToolCallStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
}

// ToolResult is the outcome of executing a tool.
type ToolResult struct {
	ToolCallID ToolCallID `json:"tool_call_id,omitempty"`
	Success    bool       `json:"success"`
	Data       any        `json:"data,omitempty"`
	Error      string     `json:"error,omitempty"`
	// Err is the typed failure carrying Op / Reason / Wrapped, populated
	// by the failure helpers (failure in zkit/ai/tools/code,
	// failedFromError in zkit/agent/runner). Guardrails should switch on
	// Err when it's non-nil rather than substring-matching Error —
	// errors.AsType and errors.Is both work natively against this field.
	Err        *Error       `json:"err,omitempty"`
	Metadata   ToolMetadata `json:"metadata,omitempty"`
	Effects    []Effect     `json:"effects,omitempty"`
	ExecutedAt time.Time    `json:"executed_at"`
}

// AddEffect appends e to r. A nil receiver is ignored so callers can use
// it defensively around optional tool results.
// DataAs returns r.Data as T when it already has that dynamic type.
func DataAs[T any](r *ToolResult) (T, bool) {
	var zero T
	if r == nil {
		return zero, false
	}
	v, ok := r.Data.(T)
	return v, ok
}

func (r *ToolResult) AddEffect(e Effect) {
	if r == nil {
		return
	}
	r.Effects = append(r.Effects, e)
}

// FileEffects returns every file payload on r, preserving effect order.
// The returned slice is a copy and may be mutated by the caller.
func (r *ToolResult) FileEffects() []FileEffect {
	if r == nil || len(r.Effects) == 0 {
		return nil
	}
	out := make([]FileEffect, 0, len(r.Effects))
	for _, e := range r.Effects {
		if e.Kind != EffectFile || e.File == nil {
			continue
		}
		out = append(out, *e.File)
	}
	return out
}

// ProcessEffects returns every process payload on r, preserving effect
// order. The returned slice is a copy and may be mutated by the caller.
func (r *ToolResult) ProcessEffects() []ProcessEffect {
	if r == nil || len(r.Effects) == 0 {
		return nil
	}
	out := make([]ProcessEffect, 0, len(r.Effects))
	for _, e := range r.Effects {
		if e.Kind != EffectProcess || e.Process == nil {
			continue
		}
		out = append(out, *e.Process)
	}
	return out
}

// ToolSpec is the LLM-facing description of a tool. The Definition() method
// on Tool returns one.
type ToolSpec struct {
	Name        ToolName `json:"name"`
	Description string   `json:"description"`
	// Parameters is the tool's input JSON Schema. Typed tools derive it
	// from their Args struct via SchemaFor; tools whose schema comes from
	// an external source (e.g. an MCP server) ingest it via llm.SchemaFromMap,
	// whose Extra field preserves rich features (anyOf, array items,
	// minimum/maximum) so they reach the LLM without a lossy round-trip.
	// The zero Schema means the tool takes no arguments.
	Parameters llm.Schema `json:"parameters"`
	// Mutates declares that a successful call produces a durable FILE edit —
	// write / edit / write_append / apply_patch, or a meta-tool that rewrites
	// the registry. This is the narrow "would show up in a diff / counts as
	// work" signal: the completion gate's empty-patch guard and spawn's verify
	// mode gate on it. A shell tool like bash leaves this false — running a
	// build or test is not a file edit — and declares AffectsWorkspace instead.
	Mutates bool `json:"mutates,omitempty"`
	// AffectsWorkspace declares that executing the tool can change durable
	// state by some means OTHER than a tracked file edit — the canonical case
	// is bash, whose command may write files, mutate git state, or touch the
	// environment. It is the broad "treat conservatively" signal: cache
	// invalidation, plan-first gating, and read-only explore blocking gate on
	// ChangesWorkspace (Mutates OR this), so a file edit need only set Mutates
	// and is still caught. Pure-read tools leave both false.
	AffectsWorkspace bool `json:"affects_workspace,omitempty"`
}

// ChangesWorkspace reports whether a successful call could alter durable
// state by any means — a tracked file edit (Mutates) or a side effect like a
// shell command (AffectsWorkspace). It is the conservative superset every
// "did this touch the workspace?" consumer should use, so a new mutating tool
// that declares only Mutates is gated without a hardcoded name list.
func (s ToolSpec) ChangesWorkspace() bool {
	return s.Mutates || s.AffectsWorkspace
}

// ToolPreference captures hints about a tool — enabled, weight for selection,
// optional parameter overrides — used by upstream selectors.
type ToolPreference struct {
	Tool       ToolName       `json:"tool"`
	Enabled    bool           `json:"enabled"`
	Weight     float64        `json:"weight"`
	Parameters ToolParameters `json:"parameters"`
	Reason     string         `json:"reason"`
}

// Tool is the canonical interface every tool implements. Two methods only:
//
//   - Definition: how the tool describes itself to an LLM (name, params schema)
//   - Execute:    runs the tool against a Call and returns a Result
//
// ParseLLMResponse / CreateToolCalls / per-tool plumbing methods that used to
// live on tool implementations have been moved to the Registry — concrete
// tools are pure execution logic.
type Tool interface {
	Definition() ToolSpec
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
}

// Registry manages tool registration and lookup. Tools are addressed by
// ToolName. The Version field bumps on every mutation so consumers (e.g.
// spec caches) can invalidate their state lazily.
//
// A Registry can also apply DescriptionStore overrides to its ToolSpecs
// output — operators edit tool descriptions live without code changes.
// The store is set via SetDescriptionStore; for cache invalidation,
// register the Registry as a bumper on the store via store.AddBumper(reg).
//
// Tools may also be tagged with a "provider" — a string identifying
// where they came from (e.g. "obsidian" for tools discovered from an
// MCP server, "homeassistant" for tools synthesized from HA entities).
// Provider grouping lets profile systems whitelist all tools from a
// source without enumerating tool names individually.
type Registry struct {
	mu        sync.RWMutex
	tools     map[ToolName]Tool
	providers map[ToolName]string // tool name → provider tag
	version   int
	specs     []ToolSpec // memoized ToolSpecs output; nil = stale
	descStore DescriptionStore
}

// NewRegistry creates a registry. Pass tools to register them inline:
//
//	reg := tools.NewRegistry(&myTool{}, &otherTool{})
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{
		tools:     make(map[ToolName]Tool),
		providers: make(map[ToolName]string),
	}
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

// Register adds a tool to the registry. Tools are addressed by their
// Definition().Name; subsequent registrations under the same name
// replace. The tool has no provider tag.
func (r *Registry) Register(tool Tool) {
	r.register(tool, "")
}

// RegisterWithProvider adds a tool tagged with a provider name. Useful
// for tools discovered from a third-party source (MCP server, HA
// entity sync, etc.) so profiles can whitelist by provider rather than
// enumerating individual tool names.
func (r *Registry) RegisterWithProvider(tool Tool, provider string) {
	r.register(tool, provider)
}

func (r *Registry) register(tool Tool, provider string) {
	name := tool.Definition().Name
	r.mu.Lock()
	r.tools[name] = tool
	if provider != "" {
		r.providers[name] = provider
	} else {
		delete(r.providers, name)
	}
	r.version++
	r.invalidateSpecsLocked()
	r.mu.Unlock()
}

// Unregister removes a tool by name.
func (r *Registry) Unregister(name ToolName) {
	r.mu.Lock()
	delete(r.tools, name)
	delete(r.providers, name)
	r.version++
	r.invalidateSpecsLocked()
	r.mu.Unlock()
}

// UnregisterProvider removes every tool tagged with the given provider.
// No-op if no tools match.
func (r *Registry) UnregisterProvider(provider string) {
	r.mu.Lock()
	removed := false
	for name, p := range r.providers {
		if p == provider {
			delete(r.tools, name)
			delete(r.providers, name)
			removed = true
		}
	}
	if removed {
		r.version++
		r.invalidateSpecsLocked()
	}
	r.mu.Unlock()
}

// ProviderFor returns the provider tag a tool was registered under, or
// "" if the tool is unregistered or has no provider.
func (r *Registry) ProviderFor(name ToolName) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[name]
}

// ToolsByProvider returns every tool tagged with the given provider.
func (r *Registry) ToolsByProvider(provider string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Tool
	for name, p := range r.providers {
		if p == provider {
			if t, ok := r.tools[name]; ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// ToolCountForProvider returns the number of tools tagged with the
// given provider.
func (r *Registry) ToolCountForProvider(provider string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, p := range r.providers {
		if p == provider {
			count++
		}
	}
	return count
}

// Tool returns a tool by name, or false if not registered.
func (r *Registry) Tool(name ToolName) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// Tools returns every registered tool as an iter.Seq. The snapshot is
// taken under the read lock so callers can range over it without
// holding any registry locks themselves. The snapshot is sorted by
// tool name so the yield order is deterministic across calls and
// process restarts — the runner serialises this order straight into
// the request's tool list, and a stable order keeps the request's
// byte prefix identical turn-to-turn (DeepSeek / llama.cpp prefix
// caching only fires on an exact byte-prefix match; map iteration
// order would reshuffle the specs and miss the cache every turn).
func (r *Registry) Tools(ctx context.Context) iter.Seq[Tool] {
	_ = ctx
	r.mu.RLock()
	// Allocates a fresh []Tool snapshot per call (one per runner
	// iteration). Negligible for realistic tool counts; only worth
	// caching behind a generation counter if a profile ever flags it.
	snapshot := make([]Tool, 0, len(r.tools))
	store := r.descStore
	for _, t := range r.tools {
		if store != nil {
			t = applyDescriptionOverride(t, store)
		}
		snapshot = append(snapshot, t)
	}
	r.mu.RUnlock()
	slices.SortFunc(snapshot, func(a, b Tool) int {
		return cmp.Compare(a.Definition().Name, b.Definition().Name)
	})
	return func(yield func(Tool) bool) {
		for _, t := range snapshot {
			if !yield(t) {
				return
			}
		}
	}
}

// Names returns every registered tool name.
func (r *Registry) Names() []ToolName {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolName, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// Version returns the monotonically-increasing version, bumped on mutation.
func (r *Registry) Version() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// BumpVersion increments the version counter and invalidates the spec
// memo. Satisfies InvalidationBumper so a DescriptionStore can notify
// the Registry when overrides change.
func (r *Registry) BumpVersion() {
	r.mu.Lock()
	r.version++
	r.invalidateSpecsLocked()
	r.mu.Unlock()
}

// SetDescriptionStore installs a DescriptionStore the Registry consults
// when building ToolSpecs. Each spec's Description is overridden by the
// store's entry for that tool name when one exists.
func (r *Registry) SetDescriptionStore(store DescriptionStore) {
	r.mu.Lock()
	r.descStore = store
	r.invalidateSpecsLocked()
	r.mu.Unlock()
}

// ToolSpecs returns the LLM specs for every registered tool, sorted by
// name, with DescriptionStore overrides applied — derived through the
// same path as Tools() so the two views cannot drift. The result is
// memoized until the next registration or description change.
func (r *Registry) ToolSpecs() []ToolSpec {
	r.mu.RLock()
	memo := r.specs
	r.mu.RUnlock()
	if memo != nil {
		return memo
	}

	// Build OUTSIDE the lock: Tools() takes its own RLock (sorted
	// snapshot, overrides applied via applyDescriptionOverride). Two
	// concurrent rebuilds race benignly — same content, last write wins.
	var specs []ToolSpec
	for t := range r.Tools(context.Background()) {
		specs = append(specs, t.Definition())
	}

	r.mu.Lock()
	r.specs = specs
	r.mu.Unlock()
	return specs
}

// ParseCall builds a ToolCall envelope for the named tool. The arguments are
// not interpreted — concrete tools validate their own argument shape during
// Execute. Returns an error if the named tool isn't registered.
func (r *Registry) ParseCall(name ToolName, callID ToolCallID, arguments map[string]any) (ToolCall, error) {
	if _, ok := r.Tool(name); !ok {
		return ToolCall{}, fmt.Errorf("tool not found: %s", name)
	}
	args := ToolParameters{}
	maps.Copy(args, arguments)
	return ToolCall{
		ID:        callID,
		ToolName:  name,
		Arguments: args,
		Status:    ToolCallStatusPending,
		CreatedAt: time.Now(),
	}, nil
}

// Execute dispatches a call to the registered tool. Returns an error if the
// tool isn't registered.
func (r *Registry) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	tool, ok := r.Tool(call.ToolName)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", call.ToolName)
	}
	return tool.Execute(ctx, call)
}

// invalidateSpecsLocked drops the ToolSpecs memo. Callers must hold r.mu.
func (r *Registry) invalidateSpecsLocked() { r.specs = nil }
