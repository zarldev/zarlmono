package taskscope

import "context"

// ID is the stable identifier for an agent task/run.
type ID string

type contextKey int

const (
	keyID contextKey = iota
	keyDepth
	keyWorkMode
)

// WithID returns a context carrying the current task ID.
func WithID(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, keyID, id)
}

// IDFrom returns the current task ID, or the empty ID when the context
// is not inside a task-scoped runner/tool dispatch.
func IDFrom(ctx context.Context) ID {
	v, _ := ctx.Value(keyID).(ID)
	return v
}

// WithDepth returns a context carrying the current spawn-agent recursion depth.
func WithDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, keyDepth, depth)
}

// DepthFrom returns the current spawn-agent recursion depth, or 0 when
// the context is not inside a task-scoped runner/tool dispatch.
func DepthFrom(ctx context.Context) int {
	v, _ := ctx.Value(keyDepth).(int)
	return v
}

// WithWorkMode returns a context carrying the current run's [WorkMode].
// The spawn-agent tool plants it on a sub-agent's Run ctx alongside the
// tool gate, so per-call policy layers (the shell guardrail's verify
// profile) can apply mode-specific rules without a back-channel. An
// invalid mode (the zero NONE included) is not planted.
func WithWorkMode(ctx context.Context, mode WorkMode) context.Context {
	if !mode.IsValid() {
		return ctx
	}
	return context.WithValue(ctx, keyWorkMode, mode)
}

// WorkModeFrom returns the current run's [WorkMode], or the zero
// WorkModes.NONE when the context is not inside a mode-scoped sub-agent
// run (top-level runs have no mode).
func WorkModeFrom(ctx context.Context) WorkMode {
	v, _ := ctx.Value(keyWorkMode).(WorkMode)
	return v
}
