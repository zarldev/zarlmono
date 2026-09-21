package engine_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestWaitingInteractiveTurnCanBeCanceled(t *testing.T) {
	t.Parallel()
	provider := &blockingProvider{started: make(chan struct{})}
	live := reservationRunner(t, provider)
	synctest.Test(t, func(t *testing.T) {
		firstCtx, stopFirst := context.WithCancel(t.Context())
		firstDone := make(chan error, 1)
		go func() { firstDone <- live.RunTurn(firstCtx, "held turn") }()
		defer func() { stopFirst(); <-firstDone }()
		<-provider.started

		secondCtx, stopSecond := context.WithCancel(t.Context())
		secondDone := make(chan error, 1)
		go func() { secondDone <- live.RunTurn(secondCtx, "canceled waiter") }()
		defer stopSecond()
		synctest.Wait()
		stopSecond()
		synctest.Wait()
		select {
		case err := <-secondDone:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("waiting turn: %v", err)
			}
		default:
			stopFirst()
			<-secondDone
			t.Fatal("canceled waiter remained blocked by first turn")
		}
	})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatalf("canceled waiter retained admission: %v", err)
	}
	reservation.Release()
}
