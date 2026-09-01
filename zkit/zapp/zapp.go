package zapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
)

const (
	// ExitOK is the default successful process exit code.
	ExitOK = 0

	// ExitFailure is the default process exit code for expected failures.
	ExitFailure = 1

	// ExitPanic is the default process exit code for recovered panics.
	ExitPanic = 2

	defaultShutdownTimeout = 30 * time.Second
)

// Registration and lifecycle errors.
var (
	ErrDuplicateName = errors.New("zapp: duplicate resource name")
	ErrClosed        = errors.New("zapp: app is closing or closed")
)

// Program defines the lifecycle for a command-line application.
//
// Create builds the typed application instance and may register resources with
// AddCloser. Run executes the application and returns the desired process exit
// code. Name identifies the program in errors and metadata.
type Program[T any] interface {
	Name() string
	Create(context.Context, *App[T]) (T, error)
	Run(context.Context, *App[T], T) int
}

// PanicHandler observes a panic recovered by App.Run.
type PanicHandler func(appName string, recovered any)

// CloseFunc adapts a cleanup function to [io.Closer] so callers can
// register function-shaped cleanup with [App.AddCloser] without defining
// local adapter types.
type CloseFunc func() error

// Close invokes f.
func (f CloseFunc) Close() error {
	return f()
}

// ContextCloser releases a resource while honoring the caller's shutdown
// deadline. Long-running cleanup should implement this contract rather than
// hiding its own timeout inside an io.Closer.
type ContextCloser interface {
	Close(context.Context) error
}

// ContextCloseFunc adapts a context-aware cleanup function to [ContextCloser].
type ContextCloseFunc func(context.Context) error

// Close invokes f.
func (f ContextCloseFunc) Close(ctx context.Context) error {
	return f(ctx)
}

// App wraps a Program with signal handling, panic recovery, and deterministic
// resource cleanup.
type App[T any] struct {
	name    string
	program Program[T]

	closers map[string]registeredCloser
	order   []string
	closing bool
	mu      sync.Mutex

	shutdownTimeout    time.Duration
	signals            []os.Signal
	createFailureCode  int
	cleanupFailureCode int
	panicCode          int
	panicHandler       PanicHandler
}

// New creates an App for program using sensible defaults.
func New[T any](program Program[T], opts ...options.Option[App[T]]) *App[T] {
	app := &App[T]{
		name:    program.Name(),
		program: program,
	}
	app.normaliseDefaults()

	for _, opt := range opts {
		opt(app)
	}

	return app
}

// Name returns the app's normalized program name.
func (a *App[T]) Name() string {
	return a.name
}

// Run creates and runs the program, then always attempts cleanup.
func (a *App[T]) Run(ctx context.Context) int {
	code := a.run(ctx)
	if err := a.closeWithTimeout(); err != nil && code == ExitOK {
		code = a.cleanupFailureCode
	}
	return code
}

func (a *App[T]) run(ctx context.Context) int {
	out := struct {
		code      int
		recovered any
	}{code: ExitOK}

	func() {
		defer func() { out.recovered = recover() }()
		out.code = a.runProgram(ctx)
	}()
	if out.recovered != nil {
		a.handlePanic(out.recovered)
		return a.panicCode
	}
	return out.code
}

func (a *App[T]) runProgram(ctx context.Context) int {
	var stop context.CancelFunc
	ctx, stop = signal.NotifyContext(ctx, a.signals...)
	defer stop()

	instance, err := a.program.Create(ctx, a)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: create: %v\n", a.Name(), err)
		return a.createFailureCode
	}

	// instance is threaded straight to Run; App deliberately does not
	// retain it (no shared mutable handle readable before Create).
	return a.program.Run(ctx, a, instance)
}

// AddCloser registers a named resource for cleanup.
func (a *App[T]) AddCloser(name string, closer io.Closer) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closing {
		return ErrClosed
	}
	if _, exists := a.closers[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}

	a.closers[name] = registeredCloser{
		close: func(context.Context) error { return closer.Close() },
	}
	a.order = append(a.order, name)
	return nil
}

// AddContextCloser registers a named context-aware resource for cleanup.
func (a *App[T]) AddContextCloser(name string, closer ContextCloser) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return ErrClosed
	}
	if _, exists := a.closers[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}
	a.closers[name] = registeredCloser{close: closer.Close}
	a.order = append(a.order, name)
	return nil
}

// Close closes all registered resources in reverse registration order.
func (a *App[T]) Close(ctx context.Context) error {
	entries := a.drainClosers()
	var errs []error
	for _, entry := range entries {
		if err := a.closeEntry(ctx, entry); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (a *App[T]) closeEntry(ctx context.Context, entry closerEntry) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: close %q context: %w", a.Name(), entry.name, err)
	}
	if err := entry.close(ctx); err != nil {
		return fmt.Errorf("%s: close %q: %w", a.Name(), entry.name, err)
	}
	return nil
}

func (a *App[T]) handlePanic(recovered any) {
	defer func() { _ = recover() }()
	a.panicHandler(a.Name(), recovered)
}

func (a *App[T]) normaliseDefaults() {
	a.closers = make(map[string]registeredCloser)
	a.shutdownTimeout = defaultShutdownTimeout
	a.signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	a.createFailureCode = ExitFailure
	a.cleanupFailureCode = ExitFailure
	a.panicCode = ExitPanic
}

type registeredCloser struct {
	close func(context.Context) error
}

type closerEntry struct {
	name string
	registeredCloser
}

func (a *App[T]) closeWithTimeout() error {
	ctx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	return a.Close(ctx)
}

func (a *App[T]) drainClosers() []closerEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.closing = true
	if len(a.order) == 0 {
		return nil
	}

	entries := make([]closerEntry, 0, len(a.order))
	for i := len(a.order) - 1; i >= 0; i-- {
		name := a.order[i]
		closer := a.closers[name]
		entries = append(entries, closerEntry{name: name, registeredCloser: closer})
	}

	clear(a.closers)
	a.order = nil
	return entries
}

// WithShutdownTimeout configures the maximum time allowed for cleanup.
func WithShutdownTimeout[T any](timeout time.Duration) options.Option[App[T]] {
	return func(app *App[T]) {
		app.shutdownTimeout = timeout
	}
}

// WithSignals configures the signals passed to [signal.NotifyContext].
func WithSignals[T any](signals ...os.Signal) options.Option[App[T]] {
	return func(app *App[T]) {
		app.signals = signals
	}
}

// WithCreateFailureExitCode configures the exit code returned when Create fails.
func WithCreateFailureExitCode[T any](code int) options.Option[App[T]] {
	return func(app *App[T]) {
		app.createFailureCode = code
	}
}

// WithCleanupFailureExitCode configures the exit code returned when cleanup
// fails after an otherwise successful run.
func WithCleanupFailureExitCode[T any](code int) options.Option[App[T]] {
	return func(app *App[T]) {
		app.cleanupFailureCode = code
	}
}

// WithPanicExitCode configures the exit code returned when Run recovers a panic.
func WithPanicExitCode[T any](code int) options.Option[App[T]] {
	return func(app *App[T]) {
		app.panicCode = code
	}
}

// WithPanicHandler configures a callback for panics recovered by Run.
func WithPanicHandler[T any](handler PanicHandler) options.Option[App[T]] {
	return func(app *App[T]) {
		app.panicHandler = handler
	}
}
