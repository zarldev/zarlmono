package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func runnerRateCard() llm.RateCard {
	return llm.RateCard{
		Reference:       llm.RateCardReference{ID: "operator-card", Revision: "v1", Entry: "exact-entry"},
		PricingProvider: "operator", Currency: "EUR", Billing: llm.BillingModes.BILLINGTOKENMETERED,
		Applicable: true, TokenOnly: true,
		FreshInput:  llm.TokenRate{PerMillionTokens: 2, Known: true},
		Output:      llm.TokenRate{PerMillionTokens: 3, Known: true},
		CacheWrites: []llm.CacheWriteRate{{Lifetime: time.Hour, Rate: llm.TokenRate{PerMillionTokens: 4, Known: true}}},
	}
}

type rateCardSink struct {
	runner.NopSink
	events []runner.ProviderAttemptSettled
	mutate bool
}

func (s *rateCardSink) OnProviderAttemptSettled(_ context.Context, e runner.ProviderAttemptSettled) {
	s.events = append(s.events, e)
	if s.mutate && e.RateCard != nil {
		e.RateCard.Reference.Revision = "sink-revision"
		e.RateCard.CacheWrites[0].Rate.PerMillionTokens = 999
	}
}

func TestRunnerRateCardOwnedAcrossRetriesAndRuns(t *testing.T) {
	card := runnerRateCard()
	want := card.Clone()
	var settlements []inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) {
		settlements = append(settlements, s)
	}))
	calls := 0
	client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			calls++
			if calls == 1 {
				yield(llm.CompletionChunk{}, errors.New("Failed to parse tool call arguments as JSON: bad escape"))
				return
			}
			yield(llm.CompletionChunk{Content: "answer"}, nil)
		}
	})
	sink := &rateCardSink{mutate: true}
	r := runner.New(client, runner.WithRateCard(card), runner.WithSink(sink), runner.WithInferenceScope(inference.Scope{Account: account}))
	card.Reference = llm.RateCardReference{ID: "other", Revision: "v2", Entry: "other"}
	card.FreshInput.PerMillionTokens = 100
	card.CacheWrites[0].Rate.PerMillionTokens = 100
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil || calls != 2 || len(result.ProviderObservations) != 2 || len(settlements) != 2 || len(sink.events) != 2 {
		t.Fatalf("result = %+v, calls = %d, settlements = %d, events = %d", result, calls, len(settlements), len(sink.events))
	}
	for i, observation := range result.ProviderObservations {
		if observation.Attempt != i+1 || !reflect.DeepEqual(observation.RateCard, &want) || !reflect.DeepEqual(settlements[i].Call.RateCard, &want) || observation.Usage != nil || observation.PricingUsage != nil {
			t.Fatalf("attempt %d: observation = %+v, settlement = %+v", i, observation, settlements[i])
		}
	}
	clone := result.ProviderObservations[0].Clone()
	clone.RateCard.Reference.Revision = "clone-revision"
	clone.RateCard.CacheWrites[0].Rate.PerMillionTokens = 200
	if !reflect.DeepEqual(result.ProviderObservations[0].RateCard, &want) {
		t.Fatal("observation Clone borrowed the card")
	}
	result.ProviderObservations[0].RateCard.Reference.Revision = "result-revision"
	result.ProviderObservations[0].RateCard.CacheWrites[0].Rate.PerMillionTokens = 300
	if !reflect.DeepEqual(settlements[0].Call.RateCard, &want) {
		t.Fatal("result mutation changed account caller data")
	}
	settlements[0].Call.RateCard.Reference.Revision = "account-revision"
	settlements[0].Call.RateCard.CacheWrites[0].Rate.PerMillionTokens = 400
	if result.ProviderObservations[0].RateCard.Reference.Revision != "result-revision" || result.ProviderObservations[0].RateCard.CacheWrites[0].Rate.PerMillionTokens != 300 {
		t.Fatal("account caller mutation changed the result")
	}
	if !reflect.DeepEqual(result.ProviderObservations[1].RateCard, &want) || !reflect.DeepEqual(settlements[1].Call.RateCard, &want) || sink.events[0].RateCard.CacheWrites[0].Rate.PerMillionTokens != 999 {
		t.Fatal("event, result, account or retry snapshots alias")
	}
	next := r.Run(t.Context(), runner.TaskSpec{Prompt: "next"})
	if next.Err != nil || len(next.ProviderObservations) != 1 || !reflect.DeepEqual(next.ProviderObservations[0].RateCard, &want) || result.ProviderObservations[0].RateCard.Reference.Revision != "result-revision" {
		t.Fatalf("earlier and later Run snapshots alias: %+v", next)
	}
	if got := account.Snapshot(); got.Tokens != 0 || got.Usage != (llm.Usage{}) || got.Unknown != 3 {
		t.Fatalf("card inferred usage or changed legacy aggregate: %+v", got)
	}
}

func TestRunnerRateCardTerminalAndAbsent(t *testing.T) {
	failure := errors.New("provider terminal")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "failure", err: failure},
		{name: "cancelled", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, configured := range []bool{false, true} {
				client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					return func(yield func(llm.CompletionChunk, error) bool) {
						if tc.err != nil {
							yield(llm.CompletionChunk{}, tc.err)
							return
						}
						yield(llm.CompletionChunk{Content: "answer"}, nil)
					}
				})
				sink := &rateCardSink{}
				r := runner.New(client, runner.WithSink(sink))
				var want *llm.RateCard
				if configured {
					card := runnerRateCard()
					want = &card
					r = runner.New(client, runner.WithSink(sink), runner.WithRateCard(card))
				}
				result := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"})
				if !errors.Is(result.Err, tc.err) || len(result.ProviderObservations) != 1 || len(sink.events) != 1 {
					t.Fatalf("configured=%v: result=%+v, events=%+v", configured, result, sink.events)
				}
				observation := result.ProviderObservations[0]
				if !reflect.DeepEqual(observation.RateCard, want) || !reflect.DeepEqual(sink.events[0].RateCard, want) || observation.Usage != nil || observation.PricingUsage != nil {
					t.Fatalf("card presence inferred evidence: %+v", observation)
				}
			}
		})
	}
}

func TestRunnerRateCardAdmissionRefusal(t *testing.T) {
	account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}))
	scope := inference.Scope{Account: account}
	scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {})(func(llm.CompletionChunk, error) bool { return true })
	sink := &rateCardSink{}
	client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		t.Fatal("provider constructed after refusal")
		return nil
	})
	result := runner.New(client, runner.WithRateCard(runnerRateCard()), runner.WithSink(sink), runner.WithInferenceScope(scope)).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if !errors.Is(result.Err, inference.ErrBudget) || len(result.ProviderObservations) != 0 || len(sink.events) != 0 {
		t.Fatalf("refusal fabricated observation: %+v, events = %+v", result, sink.events)
	}
}

func TestRunnerRateCardBlockedAcrossHostRevision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		card := runnerRateCard()
		wantOld := card.Clone()
		started, release := make(chan struct{}), make(chan struct{})
		client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				close(started)
				<-release
				yield(llm.CompletionChunk{Content: "old"}, nil)
			}
		})
		old := runner.New(client, runner.WithRateCard(card))
		var oldResult runner.TaskResult
		done := make(chan struct{})
		testCtx := t.Context()
		go func() {
			defer close(done)
			oldResult = old.Run(testCtx, runner.TaskSpec{Prompt: "old"})
		}()
		<-started
		synctest.Wait()
		card.Reference.Revision = "v2"
		card.CacheWrites[0].Rate.PerMillionTokens = 40
		newRunner := runner.New(runnertest.NewClient([][]llm.CompletionChunk{{{Content: "new"}}}), runner.WithRateCard(card))
		newResult := newRunner.Run(t.Context(), runner.TaskSpec{Prompt: "new"})
		close(release)
		<-done
		if oldResult.Err != nil || newResult.Err != nil || len(oldResult.ProviderObservations) != 1 || len(newResult.ProviderObservations) != 1 {
			t.Fatalf("old=%+v, new=%+v", oldResult, newResult)
		}
		if !reflect.DeepEqual(oldResult.ProviderObservations[0].RateCard, &wantOld) || !reflect.DeepEqual(newResult.ProviderObservations[0].RateCard, &card) {
			t.Fatal("host revision repriced an in-flight attempt")
		}
		unbound := runner.New(runnertest.NewClient([][]llm.CompletionChunk{{{Content: "unbound"}}})).Run(t.Context(), runner.TaskSpec{Prompt: "unbound", Depth: 1})
		if unbound.Err != nil || len(unbound.ProviderObservations) != 1 || unbound.ProviderObservations[0].RateCard != nil {
			t.Fatal("unconfigured child runner inherited a card")
		}
	})
}
