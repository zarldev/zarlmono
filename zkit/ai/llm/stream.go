package llm

// CompletionStream is a lazy, synchronous sequence of completion observations.
// It is directly rangeable with `for chunk, err := range stream`.
//
// A stream represents one invocation and is contractually one-shot and
// non-concurrent. Invoking it begins all provider work. Each yield is
// synchronous: reference-backed chunk state is borrowed only until yield
// returns, and its boolean result must propagate unchanged through middleware
// and providers. A false result stops the stream immediately and silently.
//
// Normal return means successful completion. A terminal failure is yielded
// exactly once as (CompletionChunk{}, err), followed immediately by return;
// nothing may be yielded after that error. Finish and usage metadata are
// ordinary observations rather than lifecycle sentinels.
type CompletionStream func(func(CompletionChunk, error) bool)

// CompletionMiddleware synchronously decorates a CompletionStream. Wrap assembles
// the lazy wrapper only: it must not perform invocation work, acquire resources, or
// cause external side effects. A wrapper must not buffer, asynchronously forward,
// retain borrowed chunks, or alter the downstream yield result.
type CompletionMiddleware interface {
	Wrap(CompletionStream) CompletionStream
}

// With wraps s with middleware in declaration order. For middleware A and B,
// s.With(A, B) is A.Wrap(B.Wrap(s)): entry flows A, B, s; yielded observations
// flow s, B, A; and return unwinds s, B, A.
func (s CompletionStream) With(middleware ...CompletionMiddleware) CompletionStream {
	for i := len(middleware) - 1; i >= 0; i-- {
		s = middleware[i].Wrap(s)
	}
	return s
}
