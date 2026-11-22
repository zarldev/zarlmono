package tui_test

import (
	"context"
	"testing"
	"time"
)

func TestManualCompactionTUIPanicStillJoinsAndPersistsIncurredEvidence(t *testing.T) {
	const panicValue = "private provider panic canary"
	calls := 0
	f := manualTUIFixtureWithApplications(t, func(context.Context) {
		calls++
		panic(panicValue)
	}, true)
	command := f.ui.CompactNow()
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Errorf("provider panic propagation changed: %v", got)
			}
		}()
		command()
		t.Error("provider did not panic")
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := f.ui.FlushSessionPersistence(ctx); err != nil {
		t.Fatalf("panic left manual operation unjoinable: %v", err)
	}
	assertManualSavedAndReleased(t, f, 1, calls)
}
