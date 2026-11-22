package openai_test

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func nativeCacheHTTP(t *testing.T, body string, streaming bool, host, path string) *http.Client {
	t.Helper()
	return &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != host || req.URL.Path != path {
			t.Errorf("route = %s, want host %s path %s", req.URL, host, path)
		}
		contentType := "application/json"
		if streaming {
			contentType = "text/event-stream"
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
			Body: io.NopCloser(strings.NewReader(body)), Request: req,
		}, nil
	})}
}

func nativeCacheCollect(t *testing.T, provider llm.Provider, streaming bool) []llm.CompletionChunk {
	t.Helper()
	var chunks []llm.CompletionChunk
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: streaming, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk.Clone())
	}
	return chunks
}

func nativeCacheBody(usage string, responsesAPI, streaming bool) string {
	if responsesAPI {
		response := `{"status":"completed","output":[],"usage":` + usage + `}`
		if streaming {
			return "data: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
		}
		return response
	}
	if streaming {
		return `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":` + usage + "}\n\ndata: [DONE]\n\n"
	}
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + usage + `}`
}

func nativeCacheObserve(t *testing.T, usage string, responsesAPI, streaming bool) llm.CompletionChunk {
	t.Helper()
	path := "/v1/chat/completions"
	if responsesAPI {
		path = "/v1/responses"
	}
	client := nativeCacheHTTP(t, nativeCacheBody(usage, responsesAPI, streaming), streaming, "api.openai.com", path)
	// An arbitrary model alias must not decide whether native usage is parsed.
	provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(responsesAPI), openai.WithModel(" Company/Deployment Alias-V9 "))
	chunks := nativeCacheCollect(t, provider, streaming)
	if len(chunks) == 0 {
		t.Fatal("missing metadata")
	}
	chunk := chunks[len(chunks)-1]
	if !chunk.UsageReported || chunk.Usage.CacheCreation5mTokens != 0 || chunk.Usage.CacheCreation1hTokens != 0 || chunk.Execution != nil {
		t.Fatalf("legacy usage/presence/cost = %+v", chunk)
	}
	return chunk
}

func nativeKnown(tokens int64) llm.TokenQuantity {
	return llm.TokenQuantity{Tokens: tokens, Known: true}
}

func TestNativeChatCacheWriteScalarsAndSubsets(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name, total, read, write string
			want                     int
		}{
			{name: "absent", total: "100", read: "20"},
			{name: "null", total: "100", read: "20", write: "null"},
			{name: "zero", total: "100", read: "20", write: "0"},
			{name: "positive", total: "100", read: "20", write: "70", want: 70},
			{name: "inclusive-limit", total: "100", read: "20", write: "80", want: 80},
			{name: "negative", total: "100", read: "20", write: "-1"},
			{name: "fraction", total: "100", read: "20", write: "1.5"},
			{name: "string", total: "100", read: "20", write: `"70"`},
			{name: "object", total: "100", read: "20", write: `{}`},
			{name: "bool", total: "100", read: "20", write: "true"},
			{name: "overflow", total: "100", read: "20", write: "9223372036854775808"},
			{name: "write-over-total", total: "100", read: "null", write: "101"},
			{name: "read-write-over-total", total: "100", read: "20", write: "81"},
			{name: "read-over-total", total: "100", read: "101", write: "0"},
			{name: "unknown-total", total: "null", read: "20", write: "70", want: 70},
			{name: "unknown-read", total: "100", read: "null", write: "70", want: 70},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, tc.name), func(t *testing.T) {
				t.Parallel()
				write := ""
				if tc.write != "" {
					write = `,"cache_write_tokens":` + tc.write
				}
				usage := `{"prompt_tokens":` + tc.total + `,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":` + tc.read + write + `,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}},"completion_tokens_details":{"reasoning_tokens":0}}`
				chunk := nativeCacheObserve(t, usage, false, streaming)
				if chunk.Usage.CacheCreationTokens != tc.want || chunk.PricingUsage != nil || !chunk.Usage.ReasoningTokensReported || chunk.Usage.ReasoningTokens != 0 {
					t.Fatalf("usage = %+v pricing = %+v, want write %d, no Chat sidecar", chunk.Usage, chunk.PricingUsage, tc.want)
				}
			})
		}
	}
}

func TestNativeResponsesCacheWriteEvidence(t *testing.T) {
	t.Parallel()
	unknown := llm.TokenQuantity{}
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name, details string
			write         llm.TokenQuantity
			short, long   llm.TokenQuantity
			rows, absent  bool
		}{
			{name: "absent", details: `{}`, absent: true},
			{name: "both-null", details: `{"cache_write_tokens":null,"cache_write_tokens_details":null}`, absent: true},
			{name: "aggregate-only", details: `{"cache_write_tokens":70}`, write: nativeKnown(70)},
			{name: "explicit-zero", details: `{"cache_write_tokens":0}`, write: nativeKnown(0)},
			{name: "negative", details: `{"cache_write_tokens":-1}`},
			{name: "fraction", details: `{"cache_write_tokens":1.5}`},
			{name: "string", details: `{"cache_write_tokens":"70"}`},
			{name: "object", details: `{"cache_write_tokens":{}}`},
			{name: "bool", details: `{"cache_write_tokens":true}`},
			{name: "overflow", details: `{"cache_write_tokens":9223372036854775808}`},
			{name: "aggregate-over-total", details: `{"cache_write_tokens":101}`},
			{name: "aggregate-plus-read-over-total", details: `{"cache_write_tokens":81}`},
			{name: "empty-details", details: `{"cache_write_tokens_details":{}}`, rows: true},
			{name: "malformed-details", details: `{"cache_write_tokens_details":"invalid"}`, rows: true},
			{name: "array-details", details: `{"cache_write_tokens_details":[]}`, rows: true},
			{name: "null-details", details: `{"cache_write_tokens":70,"cache_write_tokens_details":null}`, write: nativeKnown(70)},
			{name: "complete", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}`, write: nativeKnown(70), short: nativeKnown(30), long: nativeKnown(40), rows: true},
			{name: "zero-complete", details: `{"cache_write_tokens":0,"cache_write_tokens_details":{"ttl_30m_tokens":0,"ttl_12h_tokens":0}}`, write: nativeKnown(0), short: nativeKnown(0), long: nativeKnown(0), rows: true},
			{name: "short-only", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30}}`, write: nativeKnown(70), short: nativeKnown(30), rows: true},
			{name: "long-only", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_12h_tokens":40}}`, write: nativeKnown(70), long: nativeKnown(40), rows: true},
			{name: "zero-partial", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":0}}`, write: nativeKnown(70), short: nativeKnown(0), rows: true},
			{name: "null-sibling", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":null,"ttl_12h_tokens":40}}`, write: nativeKnown(70), long: nativeKnown(40), rows: true},
			{name: "invalid-sibling", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":-1,"ttl_12h_tokens":40}}`, write: nativeKnown(70), long: nativeKnown(40), rows: true},
			{name: "no-aggregate-inference", details: `{"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}`, short: nativeKnown(30), long: nativeKnown(40), rows: true},
			{name: "invalid-aggregate-no-inference", details: `{"cache_write_tokens":-1,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}`, short: nativeKnown(30), long: nativeKnown(40), rows: true},
			{name: "sum-smaller", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":39}}`, write: nativeKnown(70), rows: true},
			{name: "sum-larger", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":41}}`, write: nativeKnown(70), rows: true},
			{name: "partial-too-large", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":71}}`, write: nativeKnown(70), rows: true},
			{name: "other-partial-too-large", details: `{"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_12h_tokens":71}}`, write: nativeKnown(70), rows: true},
			{name: "write-subset-invalidates-lifetimes", details: `{"cache_write_tokens":81,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":51}}`, rows: true},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, tc.name), func(t *testing.T) {
				t.Parallel()
				details := strings.TrimSuffix(tc.details, "}")
				if details != "{" {
					details += ","
				}
				details += `"cached_tokens":20}`
				usage := `{"input_tokens":100,"output_tokens":7,"total_tokens":107,"input_tokens_details":` + details + `,"output_tokens_details":{"reasoning_tokens":0}}`
				chunk := nativeCacheObserve(t, usage, true, streaming)
				if chunk.Usage.CacheCreationTokens != int(tc.write.Tokens) || chunk.Usage.CachedTokens != 20 || !chunk.Usage.ReasoningTokensReported || chunk.Usage.ReasoningTokens != 0 {
					t.Fatalf("legacy usage = %+v", chunk.Usage)
				}
				if tc.absent {
					if chunk.PricingUsage != nil {
						t.Fatalf("absent fields created sidecar: %+v", chunk.PricingUsage)
					}
					return
				}
				pricing := chunk.PricingUsage
				if pricing == nil {
					t.Fatal("present cache-write evidence has no sidecar")
				}
				if pricing.Input.Total != nativeKnown(100) || pricing.Input.CacheRead != nativeKnown(20) || pricing.Input.CacheWrite != tc.write || pricing.Output.Total != nativeKnown(7) || pricing.Input.Other != unknown || pricing.Output.Other != unknown || pricing.Input.PartitionKnown || !pricing.AdditionalChargesPossible {
					t.Fatalf("pricing = %+v", pricing)
				}
				var wantRows []llm.CacheWriteQuantity
				if tc.rows {
					wantRows = []llm.CacheWriteQuantity{{Lifetime: 30 * time.Minute, Quantity: tc.short}, {Lifetime: 12 * time.Hour, Quantity: tc.long}}
				}
				if !slices.Equal(pricing.Input.WritesByLifetime, wantRows) {
					t.Fatalf("lifetimes = %+v, want %+v", pricing.Input.WritesByLifetime, wantRows)
				}
			})
		}
	}
}

func TestNativeResponsesPresenceAndReadBounds(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name, input, read, output string
			total, cached, completion llm.TokenQuantity
			write, short, long        llm.TokenQuantity
			legacyRead                int
		}{
			{name: "absent", write: nativeKnown(70), short: nativeKnown(30), long: nativeKnown(40)},
			{name: "null", input: "null", read: "null", output: "null", write: nativeKnown(70), short: nativeKnown(30), long: nativeKnown(40)},
			{name: "read-absent", input: "100", output: "7", total: nativeKnown(100), completion: nativeKnown(7), write: nativeKnown(70), short: nativeKnown(30), long: nativeKnown(40)},
			{name: "zero", input: "0", read: "0", output: "0", total: nativeKnown(0), cached: nativeKnown(0), completion: nativeKnown(0)},
			{name: "read-over-total", input: "100", read: "101", output: "7", total: nativeKnown(100), completion: nativeKnown(7), legacyRead: 101},
			{name: "negative-legacy-values", input: "-1", read: "-2", output: "-3", legacyRead: -2, write: nativeKnown(70), short: nativeKnown(30), long: nativeKnown(40)},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, tc.name), func(t *testing.T) {
				t.Parallel()
				var fields []string
				for _, field := range []struct{ key, value string }{{"input_tokens", tc.input}, {"output_tokens", tc.output}} {
					if field.value != "" {
						fields = append(fields, `"`+field.key+`":`+field.value)
					}
				}
				read := ""
				if tc.read != "" {
					read = `"cached_tokens":` + tc.read + ","
				}
				fields = append(fields, `"input_tokens_details":{`+read+`"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}`)
				chunk := nativeCacheObserve(t, "{"+strings.Join(fields, ",")+"}", true, streaming)
				pricing := chunk.PricingUsage
				if pricing == nil || pricing.Input.Total != tc.total || pricing.Input.CacheRead != tc.cached || pricing.Output.Total != tc.completion || pricing.Input.CacheWrite != tc.write || chunk.Usage.CachedTokens != tc.legacyRead || chunk.Usage.CacheCreationTokens != int(tc.write.Tokens) {
					t.Fatalf("legacy = %+v pricing = %+v", chunk.Usage, pricing)
				}
				wantRows := []llm.CacheWriteQuantity{{Lifetime: 30 * time.Minute, Quantity: tc.short}, {Lifetime: 12 * time.Hour, Quantity: tc.long}}
				if !slices.Equal(pricing.Input.WritesByLifetime, wantRows) {
					t.Fatalf("lifetimes = %+v, want %+v", pricing.Input.WritesByLifetime, wantRows)
				}
			})
		}
	}
}

func TestNativeResponsesLifetimeScalarValidation(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		for _, scalar := range []string{"null", "-1", "1.5", `"30"`, "{}", "true", "9223372036854775808"} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, scalar), func(t *testing.T) {
				t.Parallel()
				for _, shortInvalid := range []bool{false, true} {
					short, long := "30", scalar
					wantShort, wantLong := nativeKnown(30), llm.TokenQuantity{}
					if shortInvalid {
						short, long = scalar, "40"
						wantShort, wantLong = llm.TokenQuantity{}, nativeKnown(40)
					}
					usage := `{"input_tokens":100,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":` + short + `,"ttl_12h_tokens":` + long + `}}}`
					chunk := nativeCacheObserve(t, usage, true, streaming)
					wantRows := []llm.CacheWriteQuantity{{Lifetime: 30 * time.Minute, Quantity: wantShort}, {Lifetime: 12 * time.Hour, Quantity: wantLong}}
					if chunk.PricingUsage == nil || chunk.PricingUsage.Input.CacheWrite != nativeKnown(70) || !slices.Equal(chunk.PricingUsage.Input.WritesByLifetime, wantRows) {
						t.Fatalf("pricing = %+v", chunk.PricingUsage)
					}
				}
			})
		}
	}
}

func TestNativeCacheOfficialSampleAndEstimatorCoverage(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		usage := `{"input_tokens":12000,"output_tokens":100,"total_tokens":12100,"input_tokens_details":{"cached_tokens":4000,"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}`
		chunk := nativeCacheObserve(t, usage, true, streaming)
		pricing := chunk.PricingUsage
		if chunk.Usage.PromptTokens != 12000 || chunk.Usage.CachedTokens != 4000 || chunk.Usage.CacheCreationTokens != 7000 || pricing == nil || pricing.Input.CacheWrite != nativeKnown(7000) {
			t.Fatalf("official sample: %+v", chunk)
		}
		wantRows := []llm.CacheWriteQuantity{{Lifetime: 30 * time.Minute, Quantity: nativeKnown(3000)}, {Lifetime: 12 * time.Hour, Quantity: nativeKnown(4000)}}
		if !slices.Equal(pricing.Input.WritesByLifetime, wantRows) {
			t.Fatalf("official sample lifetimes = %+v", pricing.Input.WritesByLifetime)
		}
		card := llm.RateCard{
			Reference:       llm.RateCardReference{ID: "test", Revision: "1", Entry: "sample"},
			PricingProvider: "test", Currency: "USD", Billing: llm.BillingModes.BILLINGTOKENMETERED,
			Applicable: true, TokenOnly: true,
			FreshInput: llm.TokenRate{Known: true, PerMillionTokens: 1}, CacheRead: llm.TokenRate{Known: true, PerMillionTokens: 2}, Output: llm.TokenRate{Known: true, PerMillionTokens: 3},
			CacheWrites: []llm.CacheWriteRate{{Lifetime: 30 * time.Minute, Rate: llm.TokenRate{Known: true, PerMillionTokens: 4}}, {Lifetime: 12 * time.Hour, Rate: llm.TokenRate{Known: true, PerMillionTokens: 5}}},
		}
		estimate := backends.EstimatePrice(card, *pricing)
		if estimate.Complete || estimate.InputAmount != 0 || estimate.OutputAmount != 0 || len(estimate.Issues) == 0 {
			t.Fatalf("provider evidence invented complete pricing: %+v", estimate)
		}
		// Only this test-owned completed fixture asserts the missing coverage.
		complete := pricing.Clone()
		complete.Input.PartitionKnown = true
		complete.Input.Other = nativeKnown(0)
		complete.Output.Other = nativeKnown(0)
		complete.AdditionalChargesPossible = false
		estimate = backends.EstimatePrice(card, complete)
		const want = (1000*1 + 4000*2 + 3000*4 + 4000*5 + 100*3) / 1_000_000.0
		if !estimate.Complete || math.Abs(estimate.Subtotal-want) > 1e-12 {
			t.Fatalf("exact lifetimes/double-counting: %+v want %v", estimate, want)
		}
		chat := nativeCacheObserve(t, strings.ReplaceAll(strings.ReplaceAll(usage, "input_tokens", "prompt_tokens"), "output_tokens", "completion_tokens"), false, streaming)
		if chat.Usage.CacheCreationTokens != 7000 || chat.PricingUsage != nil {
			t.Fatalf("Chat official aggregate = %+v", chat)
		}
	}
}

func TestNativeCacheMaximumIntegerArithmetic(t *testing.T) {
	t.Parallel()
	maxInt := int64(^uint(0) >> 1)
	for _, streaming := range []bool{false, true} {
		usage := `{"input_tokens":` + strconv.FormatInt(maxInt, 10) + `,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":` + strconv.FormatInt(maxInt, 10) + `,"cache_write_tokens_details":{"ttl_30m_tokens":` + strconv.FormatInt(maxInt, 10) + `,"ttl_12h_tokens":` + strconv.FormatInt(maxInt, 10) + `}}}`
		chunk := nativeCacheObserve(t, usage, true, streaming)
		if chunk.PricingUsage.Input.CacheWrite != nativeKnown(maxInt) || chunk.PricingUsage.Input.WritesByLifetime[0].Quantity.Known || chunk.PricingUsage.Input.WritesByLifetime[1].Quantity.Known {
			t.Fatalf("overflowing lifetime sum = %+v", chunk.PricingUsage)
		}
		usage = strings.Replace(usage, `"cached_tokens":0`, `"cached_tokens":`+strconv.FormatInt(maxInt, 10), 1)
		chunk = nativeCacheObserve(t, usage, true, streaming)
		if chunk.PricingUsage.Input.CacheWrite.Known || chunk.Usage.CacheCreationTokens != 0 {
			t.Fatalf("overflowing read/write sum = %+v", chunk)
		}
	}
}

func TestNativeChatCacheLatestSnapshotAndFinishDrain(t *testing.T) {
	t.Parallel()
	const written = `{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":70},"completion_tokens_details":{"reasoning_tokens":3}}`
	const omitted = `{"prompt_tokens":110,"completion_tokens":8,"total_tokens":118,"prompt_tokens_details":{"cached_tokens":10}}`
	frame := func(usage string) string { return `data: {"choices":[],"usage":` + usage + "}\n\n" }
	for _, tc := range []struct {
		name, frames string
		write, input int
		reasoning    bool
	}{
		{name: "finish-with-usage", frames: `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":` + written + "}\n\n", write: 70, input: 100, reasoning: true},
		{name: "trailing-usage-only", frames: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" + frame(written), write: 70, input: 100, reasoning: true},
		{name: "latest-write-replaces", frames: frame(written) + frame(strings.Replace(written, `"cache_write_tokens":70`, `"cache_write_tokens":60`, 1)), write: 60, input: 100, reasoning: true},
		{name: "latest-omission-clears", frames: frame(written) + frame(omitted), input: 110},
		{name: "latest-zero-clears", frames: frame(written) + frame(strings.Replace(written, `"cache_write_tokens":70`, `"cache_write_tokens":0`, 1)), input: 100, reasoning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := nativeCacheHTTP(t, tc.frames+"data: [DONE]\n\n", true, "api.openai.com", "/v1/chat/completions")
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
			chunks := nativeCacheCollect(t, provider, true)
			metadataCount := 0
			for _, chunk := range chunks {
				if !chunk.UsageReported {
					continue
				}
				metadataCount++
				if chunk.Usage.CacheCreationTokens != tc.write || chunk.Usage.PromptTokens != tc.input || chunk.Usage.ReasoningTokensReported != tc.reasoning || chunk.PricingUsage != nil {
					t.Fatalf("final snapshot = %+v", chunk)
				}
			}
			if metadataCount != 1 {
				t.Fatalf("usage snapshots = %d", metadataCount)
			}
			if strings.HasPrefix(tc.name, "finish-") || tc.name == "trailing-usage-only" {
				if chunks[len(chunks)-1].FinishReason != llm.FinishReasons.STOP {
					t.Fatalf("lost finish reason: %+v", chunks)
				}
			}
		})
	}
}

func TestNativeChatCacheNoChoicesIsNarrow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, details string
		wantChunks    int
		write         int
	}{
		{name: "absent", details: `{"cached_tokens":20}`},
		{name: "null", details: `{"cached_tokens":20,"cache_write_tokens":null}`},
		{name: "negative", details: `{"cached_tokens":20,"cache_write_tokens":-1}`},
		{name: "contradiction", details: `{"cached_tokens":20,"cache_write_tokens":81}`},
		{name: "zero", details: `{"cached_tokens":20,"cache_write_tokens":0}`, wantChunks: 1},
		{name: "positive", details: `{"cached_tokens":20,"cache_write_tokens":70}`, wantChunks: 1, write: 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":` + tc.details + `}}`
			client := nativeCacheHTTP(t, body, false, "api.openai.com", "/v1/chat/completions")
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
			chunks := nativeCacheCollect(t, provider, false)
			if len(chunks) != tc.wantChunks {
				t.Fatalf("chunks = %+v, want %d", chunks, tc.wantChunks)
			}
			if len(chunks) != 0 && (!chunks[0].UsageReported || chunks[0].Usage.CacheCreationTokens != tc.write || chunks[0].FinishReason != llm.FinishReasons.UNKNOWN || chunks[0].PricingUsage != nil) {
				t.Fatalf("write-only metadata = %+v", chunks[0])
			}
		})
	}
}

func TestNativeChatCacheTTLFieldsAloneIgnored(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		usage := `{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40},"cache_creation_token_details":{"ephemeral_5m_input_tokens":30,"ephemeral_1h_input_tokens":40}}}`
		chunk := nativeCacheObserve(t, usage, false, streaming)
		if chunk.Usage.CacheCreationTokens != 0 || chunk.PricingUsage != nil {
			t.Fatalf("Chat TTL fields inferred unsupported usage: %+v", chunk)
		}
	}
}

func TestNativeResponsesTerminalUsageAndLegacyDecode(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		for _, incomplete := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/incomplete=%v", streaming, incomplete), func(t *testing.T) {
				t.Parallel()
				usage := `{"input_tokens":100,"output_tokens":7,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":70},"output_tokens_details":{"reasoning_tokens":3}}`
				body := nativeCacheBody(usage, true, streaming)
				finish := llm.FinishReasons.STOP
				if incomplete {
					body = strings.ReplaceAll(body, "completed", "incomplete")
					body = strings.Replace(body, `"output":[]`, `"incomplete_details":{"reason":"max_output_tokens"},"output":[]`, 1)
					finish = llm.FinishReasons.LENGTH
				}
				if streaming {
					// Progress usage is not a terminal snapshot, and existing single
					// terminal semantics must not become a second-terminal protocol.
					body = "data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":999}}}\n\n" + body + nativeCacheBody(`{"input_tokens":999}`, true, true)
				}
				client := nativeCacheHTTP(t, body, streaming, "api.openai.com", "/v1/responses")
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true))
				chunks := nativeCacheCollect(t, provider, streaming)
				if len(chunks) != 1 || chunks[0].Usage.CacheCreationTokens != 70 || chunks[0].Usage.PromptTokens != 100 || chunks[0].FinishReason != finish || !chunks[0].Usage.ReasoningTokensReported || chunks[0].Usage.ReasoningTokens != 3 || chunks[0].PricingUsage == nil {
					t.Fatalf("terminal metadata = %+v", chunks)
				}
			})
		}
		for _, usage := range []string{`{}`, `null`} {
			client := nativeCacheHTTP(t, nativeCacheBody(usage, true, streaming), streaming, "api.openai.com", "/v1/responses")
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true))
			chunks := nativeCacheCollect(t, provider, streaming)
			if len(chunks) != 1 || chunks[0].PricingUsage != nil || chunks[0].UsageReported != (usage == `{}`) {
				t.Fatalf("usage %s altered presence = %+v", usage, chunks)
			}
		}
		// The new optional raw fields must not weaken the existing typed legacy
		// decoder or turn an invalid canonical number into a successful stream.
		for _, usage := range []string{
			`{"input_tokens":"100","input_tokens_details":{"cache_write_tokens":70}}`,
			`{"input_tokens":100,"input_tokens_details":{"cached_tokens":"20","cache_write_tokens":70}}`,
			`{"input_tokens":100,"output_tokens_details":{"reasoning_tokens":"3"}}`,
		} {
			client := nativeCacheHTTP(t, nativeCacheBody(usage, true, streaming), streaming, "api.openai.com", "/v1/responses")
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true))
			yields, failures := 0, 0
			for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: streaming}) {
				yields++
				if err != nil {
					failures++
				}
			}
			if yields != 1 || failures != 1 {
				t.Fatalf("legacy invalid usage %s: yields=%d failures=%d", usage, yields, failures)
			}
		}
	}
}

func TestNativeCacheDialectAndCustomIsolation(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		for _, responsesAPI := range []bool{false, true} {
			usage := `{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}}`
			path := "/v1/chat/completions"
			if responsesAPI {
				usage = strings.ReplaceAll(strings.ReplaceAll(usage, "prompt_tokens", "input_tokens"), "completion_tokens", "output_tokens")
				path = "/v1/responses"
			}
			body := nativeCacheBody(usage, responsesAPI, streaming)
			for _, policy := range []string{"custom", "explicit-hosted-base", "deepseek-hosted-dialect", "litellm"} {
				if responsesAPI && policy == "litellm" {
					continue // The translated Chat dialect rejects Responses selection.
				}
				t.Run(fmt.Sprintf("stream=%v/responses=%v/%s", streaming, responsesAPI, policy), func(t *testing.T) {
					t.Parallel()
					host := "api.openai.com"
					if policy == "custom" {
						host = "gateway.invalid"
					}
					client := nativeCacheHTTP(t, body, streaming, host, path)
					var provider llm.Provider
					switch policy {
					case "custom":
						provider = openai.NewProvider("test", openai.WithBaseURL("https://gateway.invalid/v1"), openai.WithHTTPClient(client), openai.WithResponsesAPI(responsesAPI))
					case "explicit-hosted-base":
						provider = openai.NewProvider("test", openai.WithBaseURL("https://api.openai.com/v1"), openai.WithHTTPClient(client), openai.WithResponsesAPI(responsesAPI))
					case "deepseek-hosted-dialect":
						provider = openai.NewProvider("test", openai.WithDeepSeekChatDialect(true), openai.WithHTTPClient(client), openai.WithResponsesAPI(responsesAPI))
					case "litellm":
						provider = openai.NewProvider("test", openai.WithLiteLLMChat(), openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
					}
					chunks := nativeCacheCollect(t, provider, streaming)
					chunk := chunks[len(chunks)-1]
					if chunk.Usage.CachedTokens != 20 {
						t.Fatalf("legacy read changed: %+v", chunk)
					}
					switch policy {
					case "litellm":
						if chunk.Usage.CacheCreationTokens != 70 || chunk.PricingUsage == nil || len(chunk.PricingUsage.Input.WritesByLifetime) != 2 || chunk.PricingUsage.Input.WritesByLifetime[0].Lifetime != 5*time.Minute || chunk.PricingUsage.Input.WritesByLifetime[1].Lifetime != time.Hour || chunk.PricingUsage.Input.WritesByLifetime[0].Quantity.Known || chunk.PricingUsage.Input.WritesByLifetime[1].Quantity.Known {
							t.Fatalf("translated Chat normalizer changed: %+v", chunk)
						}
					case "explicit-hosted-base":
						if chunk.Usage.CacheCreationTokens != 70 || (chunk.PricingUsage != nil) != responsesAPI {
							t.Fatalf("original hosted base lost native eligibility: %+v", chunk)
						}
					default:
						if chunk.Usage.CacheCreationTokens != 0 || chunk.PricingUsage != nil {
							t.Fatalf("non-native gained cache-write evidence: %+v", chunk)
						}
					}
				})
			}
		}
	}
}

func TestNativeCacheFacadeIsolation(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		usage := `{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,"prompt_tokens_details":{"cached_tokens":20,"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}}`
		body := nativeCacheBody(usage, false, streaming)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if streaming {
				w.Header().Set("Content-Type", "text/event-stream")
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(server.Close)
		for _, provider := range []llm.Provider{
			llamacpp.NewProvider(llamacpp.WithBaseURL(server.URL)),
			ollama.NewProvider(ollama.WithBaseURL(server.URL)),
			deepseek.NewProvider("test", deepseek.WithBaseURL(server.URL)),
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, provider.Name()), func(t *testing.T) {
				t.Parallel()
				chunks := nativeCacheCollect(t, provider, streaming)
				chunk := chunks[len(chunks)-1]
				if chunk.Usage.CachedTokens != 20 || chunk.Usage.CacheCreationTokens != 0 || chunk.PricingUsage != nil {
					t.Fatalf("local/native DeepSeek facade changed: %+v", chunk)
				}
			})
		}
		responseUsage := strings.ReplaceAll(strings.ReplaceAll(usage, "prompt_tokens", "input_tokens"), "completion_tokens", "output_tokens")
		client := nativeCacheHTTP(t, nativeCacheBody(responseUsage, true, streaming), streaming, "gateway.invalid", "/v1/responses")
		gateway := litellm.NewProvider("test", litellm.WithBaseURL("https://gateway.invalid"), litellm.WithProtocol(llm.ModelProtocols.PROTOCOLRESPONSES), litellm.WithHTTPClient(client))
		chunks := nativeCacheCollect(t, gateway, streaming)
		chunk := chunks[len(chunks)-1]
		if chunk.Usage.CacheCreationTokens != 0 || chunk.PricingUsage != nil || chunk.Usage.CachedTokens != 20 {
			t.Fatalf("LiteLLM Responses gained native evidence: %+v", chunk)
		}
	}
}

func TestNativeCacheOnlyCanonicalWritePaths(t *testing.T) {
	t.Parallel()
	for _, responsesAPI := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			for _, details := range []string{
				`{"CACHE_WRITE_TOKENS":70}`,
				`{"cache_creation_tokens":70}`,
				`{"cache_write_tokens_details":{"TTL_30M_TOKENS":30,"TTL_12H_TOKENS":40}}`,
				`{"cache_write_tokens_details":{"ephemeral_5m_input_tokens":30,"ephemeral_1h_input_tokens":40}}`,
			} {
				t.Run(fmt.Sprintf("responses=%v/stream=%v/%s", responsesAPI, streaming, details), func(t *testing.T) {
					t.Parallel()
					usage := `{"prompt_tokens":100,"prompt_tokens_details":` + details + `,"cache_creation_input_tokens":70}`
					if responsesAPI {
						usage = strings.ReplaceAll(usage, "prompt_tokens", "input_tokens")
					}
					chunk := nativeCacheObserve(t, usage, responsesAPI, streaming)
					if chunk.Usage.CacheCreationTokens != 0 {
						t.Fatalf("unverified alias projected aggregate: %+v", chunk)
					}
					if chunk.PricingUsage != nil {
						if !responsesAPI || chunk.PricingUsage.Input.CacheWrite.Known {
							t.Fatalf("unverified alias projected normalized aggregate: %+v", chunk.PricingUsage)
						}
						for _, row := range chunk.PricingUsage.Input.WritesByLifetime {
							if row.Quantity.Known {
								t.Fatalf("unverified lifetime alias: %+v", row)
							}
						}
					} else if responsesAPI && strings.Contains(details, `"cache_write_tokens_details"`) {
						t.Fatal("present details must preserve unknown evidence")
					}
				})
			}
		}
	}
}
