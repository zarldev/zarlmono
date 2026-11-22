package inference_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func inferenceRateCard() llm.RateCard {
	return llm.RateCard{
		Reference:       llm.RateCardReference{ID: "configured-card", Revision: "v1", Entry: "entry"},
		PricingProvider: "operator", Currency: "EUR", Billing: llm.BillingModes.BILLINGTOKENMETERED,
		Applicable: true, TokenOnly: true,
		CacheWrites: []llm.CacheWriteRate{{Lifetime: time.Hour, Rate: llm.TokenRate{PerMillionTokens: 4, Known: true}}},
	}
}

type rateCardClient func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (c rateCardClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return c(ctx, req)
}

func TestCallRateCardOwnedBeforeConstruction(t *testing.T) {
	card := inferenceRateCard()
	want := card.Clone()
	var events []inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { events = append(events, s) }))
	scope := inference.Scope{Account: account}
	client := rateCardClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		// Provider construction is already inside admission. Reusing caller
		// storage here must not alter the admitted record.
		card.Reference.Revision = "provider-revision"
		card.CacheWrites[0].Rate.PerMillionTokens = 90
		return func(yield func(llm.CompletionChunk, error) bool) { yield(llm.CompletionChunk{}, nil) }
	})
	if err := scope.Invoke(t.Context(), inference.Call{RateCard: &card}, client, llm.CompletionRequest{}, func(stream llm.CompletionStream) {
		stream(func(llm.CompletionChunk, error) bool { return true })
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !reflect.DeepEqual(events[0].Call.RateCard, &want) {
		t.Fatalf("admission borrowed caller card: %+v", events)
	}
	events[0].Call.RateCard.Reference.Revision = "sink-revision"
	events[0].Call.RateCard.CacheWrites[0].Rate.PerMillionTokens = 100
	if card.Reference.Revision != "provider-revision" || card.CacheWrites[0].Rate.PerMillionTokens != 90 {
		t.Fatal("settlement aliases caller storage")
	}
	// A later nil call does not inherit the previous basis or infer usage.
	scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {})(func(llm.CompletionChunk, error) bool { return true })
	if len(events) != 2 || events[1].Call.RateCard != nil {
		t.Fatal("card leaked into later invocation")
	}
	if got := account.Snapshot(); got.Admitted != 2 || got.Settled != 2 || got.Unknown != 2 || got.Tokens != 0 || got.Usage != (llm.Usage{}) {
		t.Fatalf("card changed legacy aggregate: %+v", got)
	}
}

func TestCallRateCardClonedAtLazyAdmission(t *testing.T) {
	card := inferenceRateCard()
	var event inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { event = s }))
	stream := (inference.Scope{Account: account}).Wrap(t.Context(), inference.Call{RateCard: &card}, func(func(llm.CompletionChunk, error) bool) {
		card.Reference.Revision = "during-invocation"
		card.CacheWrites[0].Rate.PerMillionTokens = 99
	})
	// Wrap is lazy; its borrowed references are captured only at invocation.
	card.Reference.Revision = "at-admission"
	card.CacheWrites[0].Rate.PerMillionTokens = 8
	want := card.Clone()
	stream(func(llm.CompletionChunk, error) bool { return true })
	if !reflect.DeepEqual(event.Call.RateCard, &want) {
		t.Fatalf("card was not frozen at admission: %+v", event.Call.RateCard)
	}
}

func TestCallRateCardTerminalAndRefused(t *testing.T) {
	failure := errors.New("stream terminal")
	for _, kind := range []string{"stop", "error", "panic", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			card := inferenceRateCard()
			want := card.Clone()
			var events []inference.Settlement
			account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}), inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { events = append(events, s) }))
			scope := inference.Scope{Account: account}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			invoke := func() {
				scope.Wrap(ctx, inference.Call{RateCard: &card}, func(yield func(llm.CompletionChunk, error) bool) {
					card.CacheWrites[0].Rate.PerMillionTokens = 90
					if kind == "panic" {
						panic("provider panic")
					}
					if kind == "cancel" {
						cancel()
						yield(llm.CompletionChunk{}, context.Canceled)
						return
					}
					yield(llm.CompletionChunk{}, failure)
				})(func(llm.CompletionChunk, error) bool { return kind != "stop" })
			}
			if kind == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("panic swallowed")
						}
					}()
					invoke()
				}()
			} else {
				invoke()
			}
			if len(events) != 1 || !reflect.DeepEqual(events[0].Call.RateCard, &want) || events[0].UsageReported || (kind == "panic") != events[0].Interrupted {
				t.Fatalf("terminal lost basis: %+v", events)
			}
			var denied error
			scope.Wrap(t.Context(), inference.Call{RateCard: &card}, func(func(llm.CompletionChunk, error) bool) { t.Fatal("refused stream ran") })(func(_ llm.CompletionChunk, err error) bool { denied = err; return false })
			if !errors.Is(denied, inference.ErrBudget) || len(events) != 1 || account.Snapshot().Tokens != 0 {
				t.Fatalf("refusal fabricated settlement: %v, %+v", denied, events)
			}
		})
	}
}

func TestCallRateCardDisabledScope(t *testing.T) {
	card := inferenceRateCard()
	calls := 0
	(inference.Scope{}).Wrap(t.Context(), inference.Call{RateCard: &card}, func(yield func(llm.CompletionChunk, error) bool) {
		calls++
		yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 7}}, nil)
	})(func(chunk llm.CompletionChunk, err error) bool {
		if err != nil || chunk.Usage.TotalTokens != 7 {
			t.Fatalf("disabled scope altered stream: %+v, %v", chunk, err)
		}
		return true
	})
	if calls != 1 || !reflect.DeepEqual(card, inferenceRateCard()) {
		t.Fatal("disabled scope changed card or invocation")
	}
}
