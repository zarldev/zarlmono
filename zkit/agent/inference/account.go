// Package inference owns opt-in admission and accounting for model invocations.
// It does not own provider lifetimes or replace task-local usage events.
package inference

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrBudget identifies refusal to begin another inference invocation.
var ErrBudget = errors.New("inference budget exhausted")

// Limits controls admission and settlement thresholds. Non-positive fields disable
// that limit. Invocations denies calls at the cap and signals exhaustion after all
// admitted calls settle. Tokens is a settlement threshold: in-flight work can
// overshoot it and missing usage is not estimated.
type Limits struct {
	Invocations int
	Tokens      int
}

// Call identifies a host-selected inference endpoint and purpose. Operation is
// a host label (for example runner, compaction, planner or judge), not model text.
type Call struct{ Operation, Provider, Model string }

// Settlement describes one invoked stream, not parsing or tool execution after it.
// ID is unique within its Account. UsageReported distinguishes unknown from zero.
// Stopped means the consumer ended iteration; Interrupted means the stream did not
// return normally, including panic unwinding or an admitted stream never consumed.
type Settlement struct {
	ID                   uint64
	Call                 Call
	Member               string
	TaskID               taskscope.ID
	Depth                int
	Usage                llm.Usage
	UsageReported        bool
	Started              time.Time
	Duration             time.Duration
	Err                  error
	Stopped, Interrupted bool
}

// Snapshot is a value-only aggregate. Usage sums final reported snapshots, never
// task totals. Tokens normalizes each snapshot's total against input plus output.
type Snapshot struct {
	Admitted, Settled, Active, Denied, Unknown int
	Tokens                                     int
	Usage                                      llm.Usage
	Exhausted                                  bool
}

// Account serializes admission and settlement. It starts no goroutines and does
// not retain an unbounded per-invocation log. Its zero value is not initialized.
type Account struct {
	mu        sync.Mutex
	limits    Limits
	snapshot  Snapshot
	nextID    uint64
	done      chan struct{}
	onSettled func(context.Context, Settlement)
}

// WithLimits installs trusted host admission policy.
func WithLimits(limits Limits) options.Option[Account] { return func(a *Account) { a.limits = limits } }

// WithSettlementSink installs synchronous telemetry called after accounting
// commits, outside the account lock. It may inspect Snapshot; it must tolerate
// concurrent calls, avoid blocking and not retain the operation context.
func WithSettlementSink(sink func(context.Context, Settlement)) options.Option[Account] {
	return func(a *Account) { a.onSettled = sink }
}

// New constructs an independent account without starting work.
func New(opts ...options.Option[Account]) *Account {
	a := &Account{done: make(chan struct{})}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Snapshot returns current admission and settled consumption by value.
func (a *Account) Snapshot() Snapshot { a.mu.Lock(); defer a.mu.Unlock(); return a.snapshot }

// Exhausted closes when a token threshold is reached, or when an invocation
// threshold is reached and all admitted work has settled. It is only a wake
// signal; owners remain responsible for stopping and joining work.
func (a *Account) Exhausted() <-chan struct{} { return a.done }

// Scope lends a shared account to one logical member. Its zero value disables
// accounting. Copies share the account; no provider or permission is changed.
type Scope struct {
	Account *Account
	Member  string
}

// Wrap returns a lazy stream. Admission occurs only when invoked. Every admitted
// stream settles on return, including errors, consumer stop and panic unwind.
func (s Scope) Wrap(ctx context.Context, call Call, stream llm.CompletionStream) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if err := s.invoke(ctx, call, func(observe func(llm.CompletionStream) llm.CompletionStream) {
			observe(stream)(yield)
		}); err != nil {
			yield(llm.CompletionChunk{}, err)
		}
	}
}

func (s Scope) invoke(ctx context.Context, call Call, consume func(func(llm.CompletionStream) llm.CompletionStream)) error {
	if s.Account == nil {
		consume(func(stream llm.CompletionStream) llm.CompletionStream { return stream })
		return nil
	}
	a := s.Account
	a.mu.Lock()
	if err := context.Cause(ctx); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.snapshot.Exhausted || a.limits.Invocations > 0 && a.snapshot.Admitted >= a.limits.Invocations {
		a.exhaustLocked()
		a.snapshot.Denied++
		a.mu.Unlock()
		return ErrBudget
	}
	a.nextID++
	id := a.nextID
	a.snapshot.Admitted++
	a.snapshot.Active++
	a.mu.Unlock()
	record := Settlement{ID: id, Call: call, Member: s.Member, TaskID: taskscope.IDFrom(ctx), Depth: taskscope.DepthFrom(ctx), Started: time.Now(), Interrupted: true}
	settled := false
	settle := func() {
		if settled {
			return
		}
		settled = true
		record.Duration = time.Since(record.Started)
		if record.Err == nil {
			record.Err = context.Cause(ctx)
		}
		a.settle(ctx, record)
	}
	defer settle()
	consume(func(stream llm.CompletionStream) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			defer settle()
			stream(func(chunk llm.CompletionChunk, err error) bool {
				if chunk.UsageReported {
					record.Usage, record.UsageReported = chunk.Usage, true
				}
				if err != nil {
					record.Err = err
				}
				keep := yield(chunk, err)
				if !keep {
					record.Stopped = true
				}
				return keep
			})
			record.Interrupted = false
		}
	})
	return nil
}

func (a *Account) settle(ctx context.Context, record Settlement) {
	a.mu.Lock()
	a.snapshot.Active--
	a.snapshot.Settled++
	if record.UsageReported {
		u := record.Usage
		a.snapshot.Usage.PromptTokens += u.PromptTokens
		a.snapshot.Usage.CompletionTokens += u.CompletionTokens
		a.snapshot.Usage.TotalTokens += u.TotalTokens
		a.snapshot.Usage.CachedTokens += u.CachedTokens
		a.snapshot.Usage.CacheCreationTokens += u.CacheCreationTokens
		a.snapshot.Usage.CacheCreation5mTokens += u.CacheCreation5mTokens
		a.snapshot.Usage.CacheCreation1hTokens += u.CacheCreation1hTokens
		if a.snapshot.Settled-a.snapshot.Unknown == 1 {
			a.snapshot.Usage.ReasoningTokensReported = u.ReasoningTokensReported
		} else {
			a.snapshot.Usage.ReasoningTokensReported = a.snapshot.Usage.ReasoningTokensReported && u.ReasoningTokensReported
		}
		a.snapshot.Usage.ReasoningTokens += u.ReasoningTokens
		a.snapshot.Tokens += max(u.TotalTokens, u.PromptTokens+u.CompletionTokens)
	} else {
		a.snapshot.Unknown++
	}
	a.exhaustLocked()
	a.mu.Unlock()
	if a.onSettled != nil {
		a.onSettled(ctx, record)
	}
}

func (a *Account) exhaustLocked() {
	countExhausted := a.limits.Invocations > 0 && a.snapshot.Admitted >= a.limits.Invocations && a.snapshot.Active == 0
	tokensExhausted := a.limits.Tokens > 0 && a.snapshot.Tokens >= a.limits.Tokens
	if !a.snapshot.Exhausted && (countExhausted || tokensExhausted) {
		a.snapshot.Exhausted = true
		close(a.done)
	}
}
