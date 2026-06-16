package runner

import "context"

// PromptVars are template variables the runner threads through to a
// PromptSource on each Run. Aliased so call sites read as data-for-
// templates instead of a generic map. Accessors mirror
// tools.ToolParameters' shape but stay separate — the two represent
// different concerns (one is args from the LLM to a tool, the other
// is values for prompt rendering).
type PromptVars map[string]any

// String returns the value at key, or "" when the key is absent or
// the value is not a string.
func (v PromptVars) String(key string) string {
	s, _ := v[key].(string)
	return s
}

// Int returns the value at key, or 0 when the key is absent or the
// value is not an int (a float64 from decoded JSON does not match).
func (v PromptVars) Int(key string) int {
	n, _ := v[key].(int)
	return n
}

// Bool returns the value at key, or false when the key is absent or
// the value is not a bool.
func (v PromptVars) Bool(key string) bool {
	b, _ := v[key].(bool)
	return b
}

// PromptSource resolves the runner's system prompt. Called once at the
// top of every Run, so an implementation backed by a watched file or a
// database picks up changes between turns without the runner needing
// to know how the source produces its content.
//
// Returning an empty string with a nil error is fine — the runner
// just skips the system message.
//
// # Concurrency
//
// System runs on the runner's goroutine; concurrent Run calls invoke
// it concurrently. Implementations should be safe for parallel reads
// — file-backed sources cache or use sync.RWMutex; DB-backed sources
// are typically already safe.
type PromptSource interface {
	System(ctx context.Context, vars PromptVars) (string, error)
}

// PromptFunc adapts a plain function to the PromptSource interface,
// for one-line wrappers around an existing renderer.
type PromptFunc func(ctx context.Context, vars PromptVars) (string, error)

// System calls f itself — the prompt is re-rendered on every Run, so
// a closure over mutable state picks up changes between turns.
func (f PromptFunc) System(ctx context.Context, vars PromptVars) (string, error) {
	return f(ctx, vars)
}

// StaticPrompt returns a PromptSource that always renders the same
// string, ignoring vars. Handy for tests and for callers that compute
// the prompt up front and just want to install it.
func StaticPrompt(body string) PromptSource {
	return PromptFunc(func(_ context.Context, _ PromptVars) (string, error) {
		return body, nil
	})
}
