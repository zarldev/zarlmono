package zapp_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zapp"
)

type testInstance struct {
	value string
}

type testProgram struct {
	name      string
	createErr error
	createFn  func(context.Context, *zapp.App[*testInstance]) (*testInstance, error)
	runCode   int
	runFn     func(context.Context, *zapp.App[*testInstance], *testInstance) int
}

func (p testProgram) Name() string { return p.name }

func (p testProgram) Create(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
	if p.createFn != nil {
		return p.createFn(ctx, app)
	}
	if p.createErr != nil {
		return nil, p.createErr
	}
	return &testInstance{value: "created"}, nil
}

func (p testProgram) Run(ctx context.Context, app *zapp.App[*testInstance], instance *testInstance) int {
	if p.runFn != nil {
		return p.runFn(ctx, app, instance)
	}
	return p.runCode
}

func TestNameUsesProgramName(t *testing.T) {
	app := zapp.New(testProgram{name: " test-app "})

	if got := app.Name(); got != "test-app" {
		t.Fatalf("Name() = %q, want %q", got, "test-app")
	}
}

func TestNameFallsBackForEmptyProgramName(t *testing.T) {
	app := zapp.New(testProgram{name: "   "})

	if got := app.Name(); got != "app" {
		t.Fatalf("Name() = %q, want %q", got, "app")
	}
}

func TestRunCreatesRunsAndCleansUp(t *testing.T) {
	var closed bool
	var sawInstance bool
	program := testProgram{
		name: "test-app",
		createFn: func(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
			if err := app.AddCloser("resource", zapp.CloseFunc(func() error {
				closed = true
				return nil
			})); err != nil {
				return nil, err
			}
			return &testInstance{value: "ok"}, nil
		},
		runFn: func(ctx context.Context, app *zapp.App[*testInstance], instance *testInstance) int {
			sawInstance = instance.value == "ok"
			return zapp.ExitOK
		},
	}

	code := zapp.New(program).Run(t.Context())

	if code != zapp.ExitOK {
		t.Fatalf("Run() = %d, want %d", code, zapp.ExitOK)
	}
	if !sawInstance {
		t.Fatal("Run did not receive created instance")
	}
	if !closed {
		t.Fatal("registered closer was not closed")
	}
}

func TestCreateFailureStillCleansPartialResources(t *testing.T) {
	var closed bool
	program := testProgram{
		name: "test-app",
		createFn: func(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
			if err := app.AddCloser("partial", zapp.CloseFunc(func() error {
				closed = true
				return nil
			})); err != nil {
				return nil, err
			}
			return nil, errors.New("create failed")
		},
	}

	code := zapp.New(program, zapp.WithCreateFailureExitCode[*testInstance](7)).Run(t.Context())

	if code != 7 {
		t.Fatalf("Run() = %d, want %d", code, 7)
	}
	if !closed {
		t.Fatal("partial create resource was not closed")
	}
}

func TestRunPreservesNonZeroExitCodeWhenCleanupSucceeds(t *testing.T) {
	program := testProgram{name: "test-app", runCode: 42}

	code := zapp.New(program).Run(t.Context())

	if code != 42 {
		t.Fatalf("Run() = %d, want %d", code, 42)
	}
}

func TestCleanupFailureChangesSuccessfulRunToFailure(t *testing.T) {
	program := testProgram{
		name: "test-app",
		createFn: func(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
			return &testInstance{}, app.AddCloser("bad", zapp.CloseFunc(func() error {
				return errors.New("boom")
			}))
		},
	}

	code := zapp.New(program, zapp.WithCleanupFailureExitCode[*testInstance](9)).Run(t.Context())

	if code != 9 {
		t.Fatalf("Run() = %d, want %d", code, 9)
	}
}

func TestCleanupFailurePreservesNonZeroRunCode(t *testing.T) {
	program := testProgram{
		name: "test-app",
		createFn: func(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
			return &testInstance{}, app.AddCloser("bad", zapp.CloseFunc(func() error {
				return errors.New("boom")
			}))
		},
		runCode: 11,
	}

	code := zapp.New(program, zapp.WithCleanupFailureExitCode[*testInstance](9)).Run(t.Context())

	if code != 11 {
		t.Fatalf("Run() = %d, want %d", code, 11)
	}
}

func TestCloseRunsInReverseRegistrationOrder(t *testing.T) {
	var order []string
	app := zapp.New(testProgram{name: "test-app"})
	for _, name := range []string{"first", "second", "third"} {
		if err := app.AddCloser(name, zapp.CloseFunc(func() error {
			order = append(order, name)
			return nil
		})); err != nil {
			t.Fatalf("AddCloser(%q): %v", name, err)
		}
	}

	if err := app.Close(t.Context()); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	want := []string{"third", "second", "first"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("close order = %v, want %v", order, want)
	}
}

func TestCloseErrorIncludesAppAndResourceName(t *testing.T) {
	app := zapp.New(testProgram{name: "test-app"})
	if err := app.AddCloser("db", zapp.CloseFunc(func() error { return errors.New("boom") })); err != nil {
		t.Fatalf("AddCloser(): %v", err)
	}

	err := app.Close(t.Context())
	if err == nil {
		t.Fatal("Close() error = nil, want error")
	}
	msg := err.Error()
	for _, want := range []string{"test-app", "db", "boom"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Close() error %q does not contain %q", msg, want)
		}
	}
}

func TestAddCloserValidation(t *testing.T) {
	app := zapp.New(testProgram{name: "test-app"})
	closer := zapp.CloseFunc(func() error { return nil })

	if err := app.AddCloser("", closer); !errors.Is(err, zapp.ErrEmptyName) {
		t.Fatalf("empty name error = %v, want %v", err, zapp.ErrEmptyName)
	}
	if err := app.AddCloser("nil", nil); !errors.Is(err, zapp.ErrNilCloser) {
		t.Fatalf("nil closer error = %v, want %v", err, zapp.ErrNilCloser)
	}
	if err := app.AddCloser("dup", closer); err != nil {
		t.Fatalf("AddCloser first dup: %v", err)
	}
	if err := app.AddCloser("dup", closer); !errors.Is(err, zapp.ErrDuplicateName) {
		t.Fatalf("duplicate error = %v, want %v", err, zapp.ErrDuplicateName)
	}
}

func TestPanicInCreateIsRecoveredAndCleansUp(t *testing.T) {
	var closed bool
	var recovered any
	program := testProgram{
		name: "test-app",
		createFn: func(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
			if err := app.AddCloser("resource", zapp.CloseFunc(func() error {
				closed = true
				return nil
			})); err != nil {
				return nil, err
			}
			panic("create panic")
		},
	}

	code := zapp.New(
		program,
		zapp.WithPanicExitCode[*testInstance](12),
		zapp.WithPanicHandler[*testInstance](func(appName string, got any) { recovered = got }),
	).Run(t.Context())

	if code != 12 {
		t.Fatalf("Run() = %d, want %d", code, 12)
	}
	if recovered != "create panic" {
		t.Fatalf("recovered = %v, want %q", recovered, "create panic")
	}
	if !closed {
		t.Fatal("resource was not closed after panic")
	}
}

func TestPanicInRunIsRecoveredAndCleansUp(t *testing.T) {
	var closed bool
	program := testProgram{
		name: "test-app",
		createFn: func(ctx context.Context, app *zapp.App[*testInstance]) (*testInstance, error) {
			if err := app.AddCloser("resource", zapp.CloseFunc(func() error {
				closed = true
				return nil
			})); err != nil {
				return nil, err
			}
			return &testInstance{}, nil
		},
		runFn: func(ctx context.Context, app *zapp.App[*testInstance], instance *testInstance) int {
			panic("run panic")
		},
	}

	code := zapp.New(program).Run(t.Context())

	if code != zapp.ExitPanic {
		t.Fatalf("Run() = %d, want %d", code, zapp.ExitPanic)
	}
	if !closed {
		t.Fatal("resource was not closed after panic")
	}
}

func TestCloseContextCancellationBetweenClosers(t *testing.T) {
	app := zapp.New(testProgram{name: "test-app"})
	if err := app.AddCloser("first", zapp.CloseFunc(func() error { return nil })); err != nil {
		t.Fatalf("AddCloser(first): %v", err)
	}
	if err := app.AddCloser("second", zapp.CloseFunc(func() error { return nil })); err != nil {
		t.Fatalf("AddCloser(second): %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := app.Close(ctx)
	if err == nil {
		t.Fatal("Close() error = nil, want context errors")
	}
	if count := strings.Count(err.Error(), context.Canceled.Error()); count != 2 {
		t.Fatalf("context cancellation count = %d, want 2; error: %v", count, err)
	}
}

func TestNilProgramReturnsCreateFailureCode(t *testing.T) {
	code := zapp.New(nil, zapp.WithCreateFailureExitCode[*testInstance](8)).Run(t.Context())
	if code != 8 {
		t.Fatalf("Run() = %d, want %d", code, 8)
	}
}

type exampleZarlcode struct{}

type exampleCLI struct{}

func (exampleCLI) Name() string { return "zarlcode" }

func (exampleCLI) Create(context.Context, *zapp.App[*exampleZarlcode]) (*exampleZarlcode, error) {
	return &exampleZarlcode{}, nil
}

func (exampleCLI) Run(context.Context, *zapp.App[*exampleZarlcode], *exampleZarlcode) int {
	return zapp.ExitOK
}

func ExampleNew() {
	app := zapp.New(exampleCLI{})
	fmt.Println(app.Name())
	// Output: zarlcode
}
