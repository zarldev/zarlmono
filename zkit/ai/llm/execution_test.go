package llm_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestExecutionSidecarPresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chunk llm.CompletionChunk
	}{
		{name: "absent"},
		{
			name: "present unknown",
			chunk: llm.CompletionChunk{
				PricingUsage: &llm.PricingUsage{},
				Execution:    &llm.ExecutionObservation{},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clone := tc.chunk.Clone()
			if (clone.PricingUsage == nil) != (tc.chunk.PricingUsage == nil) ||
				(clone.Execution == nil) != (tc.chunk.Execution == nil) {
				t.Fatal("cloning changed sidecar presence")
			}
			if clone.UsageReported {
				t.Fatal("sidecar presence inferred legacy usage reporting")
			}
			if clone.PricingUsage != nil {
				if clone.PricingUsage.Input.Total.Known || clone.PricingUsage.Output.Total.Known ||
					clone.PricingUsage.Input.WritesByLifetime != nil {
					t.Fatalf("present zero usage gained evidence: %+v", clone.PricingUsage)
				}
			}
			if clone.Execution != nil {
				if *clone.Execution != (llm.ExecutionObservation{}) ||
					clone.Execution.AffinitySupport != llm.Supports.SUPPORTUNKNOWN {
					t.Fatalf("present zero observation gained evidence: %+v", clone.Execution)
				}
			}
		})
	}
}

func TestExecutionSidecarsKnownZero(t *testing.T) {
	chunk := llm.CompletionChunk{
		PricingUsage: &llm.PricingUsage{
			Input: llm.PricingInputUsage{
				Total:     llm.TokenQuantity{Known: true},
				CacheRead: llm.TokenQuantity{Known: true},
			},
		},
		Execution: &llm.ExecutionObservation{
			PromptCacheReadTokens: llm.TokenQuantity{Known: true},
			RetryCount:            llm.ObservedCount{Known: true},
			FallbackCount:         llm.ObservedCount{Known: true},
			ReportedCost:          llm.ReportedCost{Reported: true},
		},
	}
	clone := chunk.Clone()
	if clone.PricingUsage.Input.Total != (llm.TokenQuantity{Known: true}) ||
		clone.PricingUsage.Input.CacheRead != (llm.TokenQuantity{Known: true}) ||
		clone.Execution.PromptCacheReadTokens != (llm.TokenQuantity{Known: true}) ||
		clone.Execution.RetryCount != (llm.ObservedCount{Known: true}) ||
		clone.Execution.FallbackCount != (llm.ObservedCount{Known: true}) {
		t.Fatalf("known zero lost presence: %+v / %+v", clone.PricingUsage, clone.Execution)
	}
	if clone.PricingUsage.Output.Total.Known || clone.Execution.PromptCacheWriteTokens.Known {
		t.Fatal("known zero inferred unrelated counts")
	}
	if clone.Execution.ReportedCost != (llm.ReportedCost{Reported: true}) {
		t.Fatalf("reported zero inferred coverage or currency: %+v", clone.Execution.ReportedCost)
	}
	if clone.Execution.AffinitySupport != llm.Supports.SUPPORTUNKNOWN {
		t.Fatal("cache token counts inferred server affinity support")
	}
}

func TestExecutionSidecarsDoNotInterpretLegacyUsage(t *testing.T) {
	chunk := llm.CompletionChunk{
		Usage:         llm.Usage{PromptTokens: 40, CachedTokens: 20},
		UsageReported: true,
	}
	clone := chunk.Clone()
	if clone.PricingUsage != nil || clone.Execution != nil {
		t.Fatal("legacy usage synthesized presence-aware sidecars")
	}
}

func TestCompletionChunkExecutionCloneIsolation(t *testing.T) {
	chunk := llm.CompletionChunk{
		PricingUsage: &llm.PricingUsage{
			Input: llm.PricingInputUsage{
				Total: llm.TokenQuantity{Tokens: 80, Known: true},
				WritesByLifetime: []llm.CacheWriteQuantity{{
					Lifetime: time.Minute,
					Quantity: llm.TokenQuantity{Tokens: 8, Known: true},
				}},
			},
		},
		Execution: &llm.ExecutionObservation{
			Route:                 "tenant-route",
			RequestedModel:        "requested-alias",
			GatewayDeployment:     "opaque-deployment",
			ReportedModel:         "reported-alias",
			ReportedVersion:       "revision-2",
			GatewayVersion:        "gateway-runtime-7",
			GatewayCallID:         "gateway-call",
			ProviderCallID:        "provider-call",
			PromptCacheReadTokens: llm.TokenQuantity{Tokens: 4, Known: true},
			RetryCount:            llm.ObservedCount{Count: 2, Known: true},
			ReportedCost: llm.ReportedCost{
				Amount: 1, Currency: "EUR", Reported: true, Source: "reported-source",
			},
		},
	}
	wantExecution := *chunk.Execution
	clone := chunk.Clone()
	if clone.PricingUsage == chunk.PricingUsage || clone.Execution == chunk.Execution {
		t.Fatal("clone retained sidecar pointers")
	}
	if *clone.Execution != wantExecution {
		t.Fatalf("clone changed observation: %+v", clone.Execution)
	}
	chunk.PricingUsage.Input.Total.Tokens = 900
	chunk.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 900
	*chunk.Execution = llm.ExecutionObservation{Route: "changed-route"}
	if clone.PricingUsage.Input.Total.Tokens != 80 ||
		clone.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 8 ||
		*clone.Execution != wantExecution {
		t.Fatal("source mutation reached retained sidecars")
	}
	clone.PricingUsage.Input.WritesByLifetime[0].Lifetime = time.Hour
	clone.Execution.ReportedCost.Amount = 99
	if chunk.PricingUsage.Input.WritesByLifetime[0].Lifetime != time.Minute ||
		chunk.Execution.ReportedCost.Reported {
		t.Fatal("clone mutation reached source sidecars")
	}
}

func TestExecutionClonePreservesEmptyCollections(t *testing.T) {
	chunk := llm.CompletionChunk{
		PricingUsage: &llm.PricingUsage{
			Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{}},
		},
	}
	if clone := chunk.Clone(); clone.PricingUsage.Input.WritesByLifetime == nil {
		t.Fatal("clone changed present empty collection to nil")
	}
}

func TestAffinityIntentAndExecutionIdentityAreIndependent(t *testing.T) {
	var request llm.CompletionRequest
	if request.Affinity.Mode != llm.AffinityModes.AFFINITYNONE || request.Affinity.SessionID != "" {
		t.Fatalf("zero request gained affinity intent: %+v", request.Affinity)
	}
	request.Affinity = llm.Affinity{
		Mode: llm.AffinityModes.AFFINITYREQUIRE, SessionID: "host-session-1",
	}
	observation := llm.ExecutionObservation{
		Route: "tenant-route", RequestedModel: "fast-claude-gpt",
		GatewayDeployment: "opaque-deployment", ReportedModel: "reported-model",
		ReportedVersion: "model-version-3", GatewayVersion: "gateway-runtime-7", GatewayCallID: "gateway-call", ProviderCallID: "upstream-call",
	}
	if clone := observation.Clone(); clone != observation {
		t.Fatalf("clone reinterpreted execution identity: %+v", clone)
	}
	if observation.AffinitySupport != llm.Supports.SUPPORTUNKNOWN || observation.ReportedCost.Reported {
		t.Fatal("request intent or alias spelling inferred server support or cost")
	}
	observation.AffinitySupport = llm.Supports.SUPPORTUNSUPPORTED
	if request.Affinity.Mode != llm.AffinityModes.AFFINITYREQUIRE || request.Affinity.SessionID != "host-session-1" {
		t.Fatal("server assertion rewrote request intent")
	}
}

func TestAffinityModeGeneratedRoundTrips(t *testing.T) {
	count := 0
	for mode := range llm.AffinityModes.All() {
		count++
		t.Run(mode.String(), func(t *testing.T) {
			parsed, err := llm.ParseAffinityMode(mode.String())
			if err != nil || parsed != mode {
				t.Fatalf("parse string = %v, %v; want %v", parsed, err, mode)
			}
			parsed, err = llm.ParseAffinityMode(mode)
			if err != nil || parsed != mode {
				t.Fatalf("parse typed value = %v, %v; want %v", parsed, err, mode)
			}
			data, err := json.Marshal(mode)
			if err != nil {
				t.Fatal(err)
			}
			var decoded llm.AffinityMode
			if err := json.Unmarshal(data, &decoded); err != nil || decoded != mode {
				t.Fatalf("JSON roundtrip = %v, %v; want %v", decoded, err, mode)
			}
			data, err = mode.MarshalBinary()
			if err != nil || string(data) != mode.String() {
				t.Fatalf("binary marshal = %q, %v; want %q", data, err, mode.String())
			}
			if err := decoded.UnmarshalBinary(data); err != nil || decoded != mode {
				t.Fatalf("binary roundtrip = %v, %v; want %v", decoded, err, mode)
			}
			if err := decoded.UnmarshalBinary([]byte(`"` + mode.String() + `"`)); err != nil || decoded != mode {
				t.Fatalf("legacy binary parse = %v, %v; want %v", decoded, err, mode)
			}
			data, err = mode.MarshalText()
			if err != nil {
				t.Fatal(err)
			}
			if err := decoded.UnmarshalText(data); err != nil || decoded != mode {
				t.Fatalf("text roundtrip = %v, %v; want %v", decoded, err, mode)
			}
		})
	}
	if count != 3 {
		t.Fatalf("affinity modes = %d, want none/prefer/require", count)
	}
}

func TestAffinityModeGeneratedParserRejectsInvalidValues(t *testing.T) {
	invalid, err := llm.ParseAffinityMode("pin")
	if !errors.Is(err, llm.ErrParseAffinityMode) || invalid.IsValid() {
		t.Fatalf("parse invalid name = %v, %v", invalid, err)
	}
	for _, value := range []any{invalid, -1, 3, 0.5, "", []byte("sticky"), struct{}{}} {
		parsed, err := llm.ParseAffinityMode(value)
		if !errors.Is(err, llm.ErrParseAffinityMode) || parsed.IsValid() {
			t.Fatalf("parse invalid %T = %v, %v", value, parsed, err)
		}
	}
}
