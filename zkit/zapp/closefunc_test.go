package zapp_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zapp"
)

func TestCloseFunc(t *testing.T) {
	t.Parallel()

	called := false
	closer := zapp.CloseFunc(func() error {
		called = true
		return nil
	})
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !called {
		t.Fatal("CloseFunc did not call wrapped function")
	}
}

func TestCloseFuncReturnsWrappedError(t *testing.T) {
	t.Parallel()

	want := errors.New("boom")
	closer := zapp.CloseFunc(func() error { return want })
	if err := closer.Close(); !errors.Is(err, want) {
		t.Fatalf("Close err = %v, want %v", err, want)
	}
}

func TestCloseFuncNilIsNoop(t *testing.T) {
	t.Parallel()

	var closer zapp.CloseFunc
	if err := closer.Close(); err != nil {
		t.Fatalf("nil CloseFunc Close err = %v, want nil", err)
	}
}
