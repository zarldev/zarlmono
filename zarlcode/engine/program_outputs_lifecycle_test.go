package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProgramOutputRecoveryReplacementWaitsForContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	setMCPSetting(t, settings, prefs.KeyProgramOutputRecovery, "on")
	root := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		var receipt string
		requests := 0
		provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
			requests++
			switch requests {
			case 1:
				return modeCalls(namedModeCall("emit", "program", `{"script":"emit(\"x\" * 70000)"}`))
			case 2:
				receipt = recoveryReceipt(t, modeResult(req.Messages, "emit"))
				return modeCalls()
			case 3:
				close(started)
				<-release
				return modeCalls(namedModeCall("active-read", "program_output_read", fmt.Sprintf(`{"receipt":%q,"limit":4}`, receipt)))
			case 4:
				assertRecoveryPage(t, modeResult(req.Messages, "active-read"))
				return modeCalls()
			case 5:
				return modeCalls(namedModeCall("stale-read", "program_output_read", fmt.Sprintf(`{"receipt":%q,"limit":4}`, receipt)))
			default:
				if got := modeResult(req.Messages, "stale-read"); !strings.Contains(got, "output unavailable") {
					t.Errorf("receipt survived restore: %s", got)
				}
				return modeCalls()
			}
		})
		live := modeLive(t, root, provider, engine.WithSettings(settings))
		if err := live.RunTurn(t.Context(), "retain"); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		ctx := t.Context()
		go func() { done <- live.RunTurn(ctx, "active read") }()
		defer func() { unblock(); <-done }()
		<-started
		restored := make(chan struct{})
		go func() { live.RestoreContext(nil); close(restored) }()
		defer func() { unblock(); <-restored }()
		synctest.Wait()
		select {
		case <-restored:
			t.Fatal("restore bypassed active context gate")
		default:
		}
		unblock()
		<-restored
		// The restore can only publish after RunTurn has released its context.
		if err := live.RunTurn(t.Context(), "stale read"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestProgramOutputRecoveryCloseDeadlineDoesNotAbandonDrain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	setMCPSetting(t, settings, prefs.KeyProgramOutputRecovery, "on")
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	requests := 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		requests++
		switch requests {
		case 1:
			return modeCalls(namedModeCall("emit", "program", `{"script":"emit(\"x\" * 70000)"}`))
		case 2:
			_ = recoveryReceipt(t, modeResult(req.Messages, "emit"))
			return modeCalls()
		default:
			close(started)
			<-release // Deliberately uncooperative provider; the test owns its release.
			return modeCalls()
		}
	})
	live := modeLive(t, t.TempDir(), provider, engine.WithSettings(settings))
	if err := live.RunTurn(t.Context(), "retain"); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = live.RunTurn(ctx, "block")
	}()
	defer func() { unblock(); <-done }()
	<-started
	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if err := live.Close(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close: %v", err)
	}
	if err := live.RunTurn(t.Context(), "must reject"); err == nil {
		t.Fatal("accepted shutdown reopened admission")
	}
	select {
	case <-done:
		t.Fatal("uncooperative provider unexpectedly drained")
	default:
	}
	unblock()
	if err := live.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
