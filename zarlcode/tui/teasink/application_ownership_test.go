package teasink_test

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

func TestApplicationOwnsMutablePayloads(t *testing.T) {
	sink := teasink.NewApplicationSink(nil)
	t.Cleanup(sink.Close)
	usage := &llm.Usage{TotalTokens: 17}
	context := &runner.ContextBreakdown{ToolBytes: 42}
	paths := []string{"original"}
	plan := code.Plan{Steps: []code.PlanStep{{Text: "original"}}}
	result := map[string]any{"nested": []any{map[string]any{"value": "original"}}}
	sink.OnConversationEnded(t.Context(), runner.ConversationEnded{TotalUsage: usage})
	sink.OnIterationCompleted(t.Context(), runner.IterationCompleted{Usage: usage, Delta: usage, Context: context})
	sink.OnWorkspaceWaitStarted(t.Context(), runner.WorkspaceWaitStarted{Paths: paths})
	sink.PlanUpdated("task", plan)
	sink.OnToolCompleted(t.Context(), runner.ToolCompleted{Result: result})
	sink.OnToolCompleted(t.Context(), runner.ToolCompleted{Result: &struct{ Text string }{"borrowed pointer"}, FormattedResult: "formatted fallback"})
	usage.TotalTokens = 99
	context.ToolBytes = 99
	paths[0] = "mutated"
	plan.Steps[0].Text = "mutated"
	result["nested"].([]any)[0].(map[string]any)["value"] = "mutated"
	if err := sink.TakeApplications(t.Context()); err != nil {
		t.Fatal(err)
	}
	checks := []func(tea.Msg){
		func(msg tea.Msg) {
			if msg.(teasink.ConversationEndedMsg).TotalUsage.TotalTokens != 17 {
				t.Fatal("terminal alias")
			}
		},
		func(msg tea.Msg) {
			event := msg.(teasink.IterationCompletedMsg)
			if event.Usage.TotalTokens != 17 || event.Delta.TotalTokens != 17 || event.Context.ToolBytes != 42 {
				t.Fatal("iteration alias")
			}
		},
		func(msg tea.Msg) {
			if msg.(teasink.WorkspaceWaitStartedMsg).Paths[0] != "original" {
				t.Fatal("path alias")
			}
		},
		func(msg tea.Msg) {
			if msg.(teasink.PlanUpdatedMsg).Plan.Steps[0].Text != "original" {
				t.Fatal("plan alias")
			}
		},
		func(msg tea.Msg) {
			if msg.(teasink.ToolCompletedMsg).Result.(map[string]any)["nested"].([]any)[0].(map[string]any)["value"] != "original" {
				t.Fatal("nested alias")
			}
		},
		func(msg tea.Msg) {
			if msg.(teasink.ToolCompletedMsg).Result != "formatted fallback" {
				t.Fatal("opaque pointer retained")
			}
		},
	}
	for _, check := range checks {
		next, ok, _ := sink.NextApplication()
		if !ok || !next.Apply(check) {
			t.Fatal("missing payload")
		}
	}
}

// A full FIFO blocks flushing the preceding text. synctest.Wait establishes
// that the event has reached that block before its caller changes the inputs.
// This tests ownership at acceptance without a concurrent read/write race.
func assertApplicationOwnedBeforeFlush(t *testing.T, publish func(*teasink.Sink), mutate func(), check func(tea.Msg) bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		sink := teasink.NewApplicationSink(nil)
		t.Cleanup(sink.Close)
		for range 4096 {
			sink.AfterEvents("fill")
		}
		sink.OnContent(t.Context(), runner.Content{Delta: "pending flush"})
		var wg sync.WaitGroup
		wg.Go(func() { publish(sink) })
		synctest.Wait()
		mutate()
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		seen := false
		for !seen {
			next, ok, changed := sink.NextApplication()
			if !ok {
				<-changed
				continue
			}
			if !next.Apply(func(msg tea.Msg) { seen = check(msg) }) {
				t.Fatal("rejected payload")
			}
		}
		wg.Wait()
	})
}

func TestApplicationAttemptOwnedBeforeBlockingFlush(t *testing.T) {
	usage := &llm.Usage{TotalTokens: 17}
	pricing := &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 17}}}}}
	execution := &llm.ExecutionObservation{ReportedModel: "original"}
	card := &llm.RateCard{CacheWrites: []llm.CacheWriteRate{{Rate: llm.TokenRate{PerMillionTokens: 17}}}}
	first := time.Second
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) {
		s.OnProviderAttemptSettled(t.Context(), runner.ProviderAttemptSettled{Usage: usage, PricingUsage: pricing, Execution: execution, RateCard: card, TimeToFirstOutput: &first})
	}, func() {
		usage.TotalTokens = 99
		pricing.Input.WritesByLifetime[0].Quantity.Tokens = 99
		execution.ReportedModel = "mutated"
		card.CacheWrites[0].Rate.PerMillionTokens = 99
		first = time.Hour
	}, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.ProviderAttemptSettledMsg)
		if !ok {
			return false
		}
		if e.Usage.TotalTokens != 17 || e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 17 || e.Execution.ReportedModel != "original" || e.RateCard.CacheWrites[0].Rate.PerMillionTokens != 17 || *e.TimeToFirstOutput != time.Second {
			t.Error("attempt retained borrowed evidence")
		}
		return true
	})
}

func TestApplicationInputsOwnedBeforeBlockingFlush(t *testing.T) {
	messages := []llm.Message{{Content: "original", Parts: []llm.ContentPart{{Image: &llm.ImageData{DataURI: "original"}}}, ContinuationItems: []llm.ContinuationItem{{Data: []byte("original")}}}}
	refs := []tools.AdmissionReference{{ID: "original"}}
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) {
		s.OnInputsAdmitted(t.Context(), runner.InputsAdmitted{Messages: messages, References: refs})
	}, func() {
		messages[0].Content = "mutated"
		messages[0].Parts[0].Image.DataURI = "mutated"
		messages[0].ContinuationItems[0].Data[0] = 'X'
		refs[0].ID = "mutated"
	}, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.InputsAdmittedMsg)
		if !ok {
			return false
		}
		if e.Messages[0].Content != "original" || e.Messages[0].Parts[0].Image.DataURI != "original" || string(e.Messages[0].ContinuationItems[0].Data) != "original" || e.References[0].ID != "original" {
			t.Error("admitted inputs retained borrowed evidence")
		}
		return true
	})
}

func TestApplicationControlOwnedBeforeBlockingFlush(t *testing.T) {
	control := runner.ReplayControl{ToolDefinitions: []runner.ControlToolDefinition{{Name: "original"}}}
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) { s.RequestControlRecorded(t.Context(), control) }, func() { control.ToolDefinitions[0].Name = "mutated" }, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.RequestControlRecordedMsg)
		if !ok {
			return false
		}
		if e.Control.ToolDefinitions[0].Name != "original" {
			t.Error("control retained borrowed definitions")
		}
		return true
	})
}

func TestApplicationDiffOwnedBeforeBlockingFlush(t *testing.T) {
	before, after := []byte("before"), []byte("after")
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) { s.DiffEvent(diffrecorder.DiffEvent{Before: before, After: after}) }, func() { before[0], after[0] = 'X', 'X' }, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.DiffMsg)
		if !ok {
			return false
		}
		if string(e.Before) != "before" || string(e.After) != "after" {
			t.Error("diff retained borrowed byte images")
		}
		return true
	})
}

func TestApplicationRichResultsOwnedBeforeBlockingFlush(t *testing.T) {
	for _, name := range []string{"plan", "grep", "ls", "glob", "search", "computer", "program", "json", "opaque"} {
		t.Run(name, func(t *testing.T) {
			result, want, mutate := richApplicationResult(t, name)
			assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) {
				s.OnToolCompleted(t.Context(), runner.ToolCompleted{Result: result, FormattedResult: "formatted fallback"})
			}, mutate, func(msg tea.Msg) bool {
				e, ok := msg.(teasink.ToolCompletedMsg)
				if !ok {
					return false
				}
				if !reflect.DeepEqual(e.Result, want) {
					t.Errorf("%s result retained borrowed payload", name)
				}
				return true
			})
		})
	}
}

func richApplicationResult(t *testing.T, name string) (any, any, func()) {
	t.Helper()
	switch name {
	case "plan":
		v := code.Plan{Steps: []code.PlanStep{{Text: "original"}}}
		return v, code.Plan{Steps: []code.PlanStep{{Text: "original"}}}, func() { v.Steps[0].Text = "mutated" }
	case "grep":
		v := code.GrepResult{Hits: []code.GrepHit{{Text: "original"}}}
		return v, code.GrepResult{Hits: []code.GrepHit{{Text: "original"}}}, func() { v.Hits[0].Text = "mutated" }
	case "ls":
		var v, want code.LsResult
		for _, target := range []*code.LsResult{&v, &want} {
			if err := json.Unmarshal([]byte(`[{"name":"original"}]`), &target.Entries); err != nil {
				t.Fatal(err)
			}
		}
		return v, want, func() { v.Entries[0].Name = "mutated" }
	case "glob":
		var v, want code.GlobResult
		for _, target := range []*code.GlobResult{&v, &want} {
			if err := json.Unmarshal([]byte(`[{"path":"original"}]`), &target.Entries); err != nil {
				t.Fatal(err)
			}
		}
		return v, want, func() { v.Entries[0].Path = "mutated" }
	case "search":
		v := search.Result{Hits: []search.Hit{{Title: "original"}}, Suggestions: []string{"original"}, Failures: []search.SourceFailure{{}}}
		want := search.Result{Hits: []search.Hit{{Title: "original"}}, Suggestions: []string{"original"}, Failures: []search.SourceFailure{{}}}
		return v, want, func() {
			v.Hits[0].Title = "mutated"
			v.Suggestions[0] = "mutated"
			v.Failures[0].Source = "mutated"
		}
	case "computer":
		var v, want computer.Observation
		for _, target := range []*computer.Observation{&v, &want} {
			if err := json.Unmarshal([]byte(`{"targets":[{"id":"original","bounds":{"x":17}}],"focused_target":{"id":"original","bounds":{"x":17}},"screenshot":{"data_uri":"original"},"hints":["original"],"raw":{"nested":["original"]}}`), target); err != nil {
				t.Fatal(err)
			}
		}
		return v, want, func() {
			v.Targets[0].ID = "mutated"
			v.Targets[0].Bounds.X = 99
			v.FocusedTarget.Bounds.X = 99
			v.Screenshot.DataURI = "mutated"
			v.Hints[0] = "mutated"
			v.Raw["nested"].([]any)[0] = "mutated"
		}
	case "program":
		v := programtools.Result{Output: map[string]any{"nested": []any{"original"}}}
		return v, programtools.Result{Output: map[string]any{"nested": []any{"original"}}}, func() { v.Output.(map[string]any)["nested"].([]any)[0] = "mutated" }
	case "json":
		v := map[string]any{"nested": []any{map[string]any{"bytes": []byte("original")}}}
		want := map[string]any{"nested": []any{map[string]any{"bytes": []byte("original")}}}
		return v, want, func() { v["nested"].([]any)[0].(map[string]any)["bytes"].([]byte)[0] = 'X' }
	default:
		v := &struct{ Text string }{Text: "original"}
		return v, "formatted fallback", func() { v.Text = "mutated" }
	}
}

func TestApplicationToolParametersOwnedBeforeBlockingFlush(t *testing.T) {
	parameters := map[string]any{"nested": []any{map[string]any{"name": "original"}}}
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) { s.OnToolStarted(t.Context(), runner.ToolStarted{Parameters: parameters}) }, func() { parameters["nested"].([]any)[0].(map[string]any)["name"] = "mutated" }, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.ToolStartedMsg)
		if !ok {
			return false
		}
		if e.Parameters["nested"].([]any)[0].(map[string]any)["name"] != "original" {
			t.Error("tool parameters remained borrowed")
		}
		return true
	})
}

func TestApplicationToolPartsAndEffectsOwnedBeforeBlockingFlush(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "failed"}[failed], func(t *testing.T) {
			parts := []llm.ContentPart{{Image: &llm.ImageData{DataURI: "original"}}, {Audio: &llm.AudioData{DataURI: "original"}}, {Video: &llm.VideoData{DataURI: "original"}}}
			effects := []tools.Effect{tools.NewFileEffect(tools.FileRead, "original"), tools.NewProcessEffect("original", 17)}
			assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) {
				if failed {
					s.OnToolFailed(t.Context(), runner.ToolFailed{Parts: parts, Effects: effects})
				} else {
					s.OnToolCompleted(t.Context(), runner.ToolCompleted{Parts: parts, Effects: effects})
				}
			}, func() {
				parts[0].Image.DataURI = "mutated"
				parts[1].Audio.DataURI = "mutated"
				parts[2].Video.DataURI = "mutated"
				effects[0].File.Path = "mutated"
				effects[1].Process.Command = "mutated"
			}, func(msg tea.Msg) bool {
				var gotParts []llm.ContentPart
				var gotEffects []tools.Effect
				switch e := msg.(type) {
				case teasink.ToolCompletedMsg:
					gotParts, gotEffects = e.Parts, e.Effects
				case teasink.ToolFailedMsg:
					gotParts, gotEffects = e.Parts, e.Effects
				default:
					return false
				}
				if gotParts[0].Image.DataURI != "original" || gotParts[1].Audio.DataURI != "original" || gotParts[2].Video.DataURI != "original" || gotEffects[0].File.Path != "original" || gotEffects[1].Process.Command != "original" {
					t.Error("tool attachments/effects remained borrowed")
				}
				return true
			})
		})
	}
}

func TestApplicationAuxiliaryOwnedBeforeBlockingFlush(t *testing.T) {
	observation := engine.AuxiliaryObservation{Inference: inference.Observation{Usage: &llm.Usage{TotalTokens: 17}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 17}}}}}, Execution: &llm.ExecutionObservation{ReportedModel: "original"}}}
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) { s.AuxiliaryObserved(t.Context(), observation) }, func() {
		observation.Inference.Usage.TotalTokens = 99
		observation.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 99
		observation.Inference.Execution.ReportedModel = "mutated"
	}, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.AuxiliaryObservedMsg)
		if !ok {
			return false
		}
		if e.Observation.Inference.Usage.TotalTokens != 17 || e.Observation.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 17 || e.Observation.Inference.Execution.ReportedModel != "original" {
			t.Error("auxiliary observation remained borrowed")
		}
		return true
	})
}

func TestApplicationUsageContextPlanPathsOwnedBeforeBlockingFlush(t *testing.T) {
	for _, name := range []string{"terminal", "iteration", "plan", "paths"} {
		t.Run(name, func(t *testing.T) {
			usage := &llm.Usage{TotalTokens: 17}
			delta := &llm.Usage{TotalTokens: 18}
			context := &runner.ContextBreakdown{ToolBytes: 42}
			plan := code.Plan{Steps: []code.PlanStep{{Text: "original"}}}
			paths := []string{"original"}
			assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) {
				switch name {
				case "terminal":
					s.OnConversationEnded(t.Context(), runner.ConversationEnded{TotalUsage: usage})
				case "iteration":
					s.OnIterationCompleted(t.Context(), runner.IterationCompleted{Usage: usage, Delta: delta, Context: context})
				case "plan":
					s.PlanUpdated("task", plan)
				case "paths":
					s.OnWorkspaceWaitStarted(t.Context(), runner.WorkspaceWaitStarted{Paths: paths})
				}
			}, func() {
				usage.TotalTokens, delta.TotalTokens, context.ToolBytes = 99, 99, 99
				plan.Steps[0].Text, paths[0] = "mutated", "mutated"
			}, func(msg tea.Msg) bool {
				switch e := msg.(type) {
				case teasink.ConversationEndedMsg:
					if e.TotalUsage.TotalTokens != 17 {
						t.Error("terminal usage alias")
					}
				case teasink.IterationCompletedMsg:
					if e.Usage.TotalTokens != 17 || e.Delta.TotalTokens != 18 || e.Context.ToolBytes != 42 {
						t.Error("iteration evidence alias")
					}
				case teasink.PlanUpdatedMsg:
					if e.Plan.Steps[0].Text != "original" {
						t.Error("plan alias")
					}
				case teasink.WorkspaceWaitStartedMsg:
					if e.Paths[0] != "original" {
						t.Error("path alias")
					}
				default:
					return false
				}
				return true
			})
		})
	}
}

func TestApplicationContentIdentityOwnedBeforeBlockingFlush(t *testing.T) {
	index := 17
	assertApplicationOwnedBeforeFlush(t, func(s *teasink.Sink) {
		s.OnContent(t.Context(), runner.Content{Delta: "owned identity", ContentOutputIndex: &index})
		s.Flush()
	}, func() { index = 99 }, func(msg tea.Msg) bool {
		e, ok := msg.(teasink.ContentMsg)
		if !ok || e.Delta != "owned identity" {
			return false
		}
		if e.ContentOutputIndex == nil {
			t.Error("content identity missing")
		} else if *e.ContentOutputIndex != 17 {
			t.Errorf("content identity changed during blocked prior flush: got %d, want 17", *e.ContentOutputIndex)
		}
		return true
	})
}
