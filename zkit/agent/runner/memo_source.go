// Memoizing tool source: caches results from pure-read tools within
// the scope of a single Run, eliminating intra-turn re-reads (the
// "read the same file three times in a row" pattern) without
// stale-cache risk.
//
// Scope is per-task: each Run gets its own bucket keyed by taskscope.ID
// (planted on ctx by the runner). When the task ends the bucket is
// dropped on the next call — no cross-task pollution, no manual
// invalidation. When a successful mutating tool call lands inside the same
// task (for example write / edit / write_append / apply_patch), MemoSource
// proactively clears that task's pure-tool bucket so a subsequent read sees
// fresh workspace state. Tools that mutate state must NOT be marked pure; only
// declare a tool pure when (args ⇒ result) holds for the duration of one
// user-level turn between mutations.
// result) holds for the duration of one user-level turn.
package runner

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/cache"
)

// PureFn reports whether a tool's outputs depend only on its args
// (and stable workspace state) within one Run. Returning true opts
// the tool into memoization; false (the default for unknown tools)
// passes through every call.
type PureFn func(name tools.ToolName) bool

// PureTools returns a PureFn that whitelists the named tools. Use
// this for the common "list these exact names" wiring:
//
//	runner.PureTools("read", "ls", "grep", "list_skills", "list_agents")
func PureTools(names ...tools.ToolName) PureFn {
	set := make(map[tools.ToolName]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(name tools.ToolName) bool {
		_, ok := set[name]
		return ok
	}
}

// MemoSource wraps a ToolSource and memoizes results from tools the
// PureFn whitelists. Cache hits short-circuit the inner Execute,
// returning a clone of the original result (with the new call's
// ToolCallID). Misses dispatch as normal and store the successful
// result before returning.
//
// Buckets are per-task — keyed by the taskscope.ID planted on ctx by the
// runner. Calls from outside a Run (direct unit tests of a tool)
// land in a shared "no task" bucket, which is fine for tests and
// fine in production: those code paths don't ship the same tool call
// twice in one go anyway.
//
// # Concurrency
//
// MemoSource is safe to share across concurrent Runs. Three layers:
//
//   - The outer `mu` guards bucket creation and the hit-counter map.
//     bucketFor and bumpHit hold it; Execute does not.
//   - Each per-task bucket is a cache.MemoryCache whose own RWMutex
//     guards reads and writes — multiple Execute calls on the same
//     bucket interleave safely without holding the outer lock.
//   - The Get → inner.Execute → Set sequence in Execute is NOT atomic.
//     Two parallel calls with the same canonical signature in the same
//     task can both miss, both run inner.Execute, then both Set.
//     Result: the underlying tool runs twice (wasted work), but the
//     cache stays consistent (last writer wins, no torn state). This
//     is acceptable for pure tools — by definition the second run
//     produces the same answer — but callers wiring a non-trivial
//     PureFn whitelist for tools with expensive side-effect-free work
//     should know the memoization is best-effort under contention.
type MemoSource struct {
	inner  ToolSource
	isPure PureFn
	ledger TaskCallLedger

	mu      sync.Mutex
	buckets map[taskscope.ID]cache.Cache[string, tools.ToolResult]
	// hits[taskID][callSig] = number of times the cache served this
	// signature already. Used to escalate from silent reuse on the
	// first repeat to a loud "stop looping" Validation on the second+.
	// Without this, a model that gets nudged toward list_skills /
	// list_agents on every turn fires the discovery tool 5+ times
	// per task; the cache returns silently and the model never
	// learns it's looping.
	hits map[taskscope.ID]map[string]int
}

// NewMemoSource wraps source with per-task memoization for tools
// pure reports as pure. A nil pure function disables memoization
// entirely (every call passes through) — useful for tests that want
// to assert no caching is happening.
func NewMemoSource(source ToolSource, pure PureFn) *MemoSource {
	return &MemoSource{
		inner:   source,
		isPure:  pure,
		buckets: make(map[taskscope.ID]cache.Cache[string, tools.ToolResult]),
		hits:    make(map[taskscope.ID]map[string]int),
	}
}

// NewMemoSourceWithLedger wraps source with per-task memoization and records
// successful pure calls into ledger when non-nil. The same invalidation
// boundary applies to both cache and ledger: any successful workspace-changing
// call drops the current task's pure-call evidence.
func NewMemoSourceWithLedger(source ToolSource, pure PureFn, ledger TaskCallLedger) *MemoSource {
	m := NewMemoSource(source, pure)
	m.ledger = ledger
	return m
}

// Tools delegates to the inner source. Memoization doesn't change
// which tools the LLM sees.
func (m *MemoSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return m.inner.Tools(ctx)
}

// Execute checks the per-task cache for a previous result of this
// (tool, args) and returns a clone on hit. On miss, dispatches the
// call and stores the successful result for future calls within the
// same task.
//
// Failed results (Success=false) are never cached — a transient
// failure on the first call shouldn't poison the rest of the turn.
func (m *MemoSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	invalidates := m.invalidatesCache(ctx, call.ToolName)
	if m.isPure == nil || !m.isPure(call.ToolName) {
		result, err := m.inner.Execute(ctx, call)
		if err == nil && result != nil && result.Success && invalidates {
			m.forgetCurrentTask(ctx)
		}
		return result, err
	}
	bucket := m.bucketFor(ctx)
	key := tools.CallSignature(call)
	if hit, err := bucket.Get(ctx, key); err == nil {
		hits := m.bumpHit(ctx, key)
		// Counter shape after this bump:
		//   hits == 2 → first cache hit (model legitimately re-checked)
		//   hits >= 3 → second cache hit and beyond — loop signal
		//
		// First repeat returns the cached result quietly. Second+
		// repeats swap in a Validation error pointing at the prior
		// result so the model sees explicit feedback that it's looping.
		// Without this nudge a model that's been told (or just decides)
		// to call list_skills / list_agents / ls speculatively will
		// keep doing it every iteration; the cache returns silently
		// and the loop compounds (32-tool-call turns where 25 are
		// duplicates).
		if hits >= 3 {
			return &tools.ToolResult{
				ToolCallID: call.ID,
				Success:    false,
				Error: fmt.Sprintf(
					"duplicate call: %q with these args has already been invoked %d times this task. "+
						"The previous successful result is still valid — read it from the conversation "+
						"history instead of re-calling. If the cached value is genuinely stale, change "+
						"the args (e.g. a different path / pattern); pure tools never change output for "+
						"identical inputs within one task.",
					call.ToolName, hits-1),
				Err: tools.Validation("memo", fmt.Sprintf(
					"duplicate call: %q with these args has already been invoked %d times this task. "+
						"The previous successful result is still valid — read it from the conversation "+
						"history instead of re-calling. If the cached value is genuinely stale, change "+
						"the args (e.g. a different path / pattern); pure tools never change output for "+
						"identical inputs within one task.",
					call.ToolName, hits-1)),
			}, nil
		}
		clone := hit
		clone.ToolCallID = call.ID
		return &clone, nil
	}
	result, err := m.inner.Execute(ctx, call)
	if err == nil && result != nil && result.Success {
		if invalidates {
			m.forgetCurrentTask(ctx)
			return result, err
		}
		_ = bucket.Set(ctx, key, *result)
		// Successful miss counts as the first invocation; the next
		// identical call is the first cache hit (hits=1, silent), the
		// one after that is the loud rejection (hits=2).
		m.bumpHit(ctx, key)
		if m.ledger != nil {
			m.ledger.RecordSuccessfulPureCall(ctx, call.ToolName, call.Arguments)
		}
	}
	return result, err
}

// bumpHit increments the per-task hit counter for key and returns
// the post-increment count. The counter underpins the "first repeat
// is silent, second repeat is loud" escalation in Execute.
func (m *MemoSource) bumpHit(ctx context.Context, key string) int {
	id := taskscope.IDFrom(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hits[id] == nil {
		m.hits[id] = map[string]int{}
	}
	m.hits[id][key]++
	return m.hits[id][key]
}

// forgetCurrentTask clears the memo bucket and hit counters for the task on ctx.
// Mutating tools call this after a successful execution so later pure reads in
// the same task observe fresh workspace state.
func (m *MemoSource) forgetCurrentTask(ctx context.Context) {
	id := taskscope.IDFrom(ctx)
	m.mu.Lock()
	delete(m.buckets, id)
	delete(m.hits, id)
	m.mu.Unlock()
	if m.ledger != nil {
		m.ledger.ForgetTask(id)
	}
}

// invalidatesCache reports whether a successful call should drop the current
// task's memoized pure-read state. Any tool that changes the workspace — a
// file edit (Mutates) or a side effect like bash (AffectsWorkspace) — counts,
// via ChangesWorkspace, so there's no per-tool name special-case to drift.
func (m *MemoSource) invalidatesCache(ctx context.Context, name tools.ToolName) bool {
	for tool := range m.inner.Tools(ctx) {
		spec := tool.Definition()
		if spec.Name == name {
			return spec.ChangesWorkspace()
		}
	}
	return false
}

// bucketFor returns (creating if necessary) the per-task cache for
// the in-flight call. Buckets are created lazily so a wrapper that's
// never called doesn't allocate. Concurrent Runs share the same
// MemoSource; the outer mutex guards bucket creation only — Get and
// Set on the returned bucket are guarded by the bucket's own internal
// RWMutex (see zkit/cache.MemoryCache).
func (m *MemoSource) bucketFor(ctx context.Context) cache.Cache[string, tools.ToolResult] {
	id := taskscope.IDFrom(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.buckets[id]
	if !ok {
		bucket = cache.NewMemoryCache[string, tools.ToolResult]()
		m.buckets[id] = bucket
	}
	return bucket
}

// ForgetTask drops memo state for the given task and forwards the lifecycle
// notification to the wrapped source when it supports the same optional
// capability.
func (m *MemoSource) ForgetTask(id taskscope.ID) {
	m.mu.Lock()
	delete(m.buckets, id)
	delete(m.hits, id)
	m.mu.Unlock()
	if m.ledger != nil {
		m.ledger.ForgetTask(id)
	}
	if tf, ok := m.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		tf.ForgetTask(id)
	}
}
