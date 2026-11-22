package litellm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

const modelInfoFixture = `{"data":[{"model_name":" Company/Production Alias-V7 ","litellm_params":{"model":"azure/deployment/opaque","api_key":"PRIVATE-CANARY","api_base":"https://PRIVATE-CANARY","custom_llm_provider":"not-serving-proof"},"model_info":{"id":"deployment-one","base_model":"azure/base/pricing-key","pricing_provider":"billing-org","max_input_tokens":8192.0,"max_output_tokens":1024,"max_tokens":99999,"supported_endpoints":["/v1/chat/completions","/v1/responses","/v1/messages","/v1beta/models/{model}:generateContent","/unknown","/v1/responses"],"input_cost_per_token":0.000002,"output_cost_per_token":0,"cache_read_input_token_cost":null,"cache_creation_input_token_cost":0.000003,"pricing_overrides":["input_cost_per_token","output_cost_per_token","cache_read_input_token_cost","cache_creation_input_token_cost","unknown_secret_field"]}},{"model_name":" Company/Production Alias-V7 ","litellm_params":{"model":"unrelated"},"model_info":{"id":"deployment-two","supported_endpoints":[],"cache_read_input_token_cost":0}}]}`

func infoProvider(body string) *litellm.Provider {
	return litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}))
}

func TestModelInfoOrdinaryKeyAndTypedSnapshot(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/gateway/v1/model/info" || r.URL.RawQuery != "" {
			t.Errorf("endpoint = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ordinary-test-key" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Anthropic-Version") != "" || r.Header.Get("Client_version") != "" {
			t.Error("enrichment must use ordinary Bearer authentication without negotiation")
		}
		_, _ = io.WriteString(w, modelInfoFixture)
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL+"/gateway/"), litellm.WithProtocol(llm.ModelProtocols.PROTOCOLMESSAGES))
	before := time.Now()
	snapshot, err := p.ModelInfo(t.Context())
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 2 {
		t.Fatalf("rows = %d", len(snapshot.Rows))
	}
	row := snapshot.Rows[0]
	if row.RequestedModel != " Company/Production Alias-V7 " || row.UpstreamModel != "azure/deployment/opaque" || row.Deployment != "deployment-one" || row.BaseModel != "azure/base/pricing-key" || row.PricingProvider != "billing-org" {
		t.Fatalf("identity = %+v", row)
	}
	if row.Limits != (llm.ModelLimits{InputTokens: 8192, OutputTokens: 1024}) {
		t.Fatalf("limits = %+v", row.Limits)
	}
	if !slices.Equal(row.SupportedProtocols, []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT}) {
		t.Fatalf("protocols = %v", row.SupportedProtocols)
	}
	if row.Prices.Input != (llm.TokenRate{PerMillionTokens: 2, Known: true}) || row.Prices.Output != (llm.TokenRate{Known: true}) || row.Prices.CacheRead.Known || row.Prices.CacheWrite != (llm.TokenRate{PerMillionTokens: 3, Known: true}) {
		t.Fatalf("prices = %+v", row.Prices)
	}
	if row.PricingOverrides != (litellm.ModelInfoPricingOverrides{Input: true, Output: true, CacheRead: true, CacheWrite: true, Known: true}) {
		t.Fatalf("overrides = %+v", row.PricingOverrides)
	}
	second := snapshot.Rows[1]
	if second.Deployment != "deployment-two" || second.UpstreamModel != "unrelated" || second.Prices.CacheRead != (llm.TokenRate{Known: true}) || second.SupportedProtocols == nil || second.Prices.Input.Known || second.BaseModel != "" {
		t.Fatalf("second row = %+v", second)
	}
	sum := sha256.Sum256([]byte(modelInfoFixture))
	if snapshot.Provenance.Source != llm.MetadataSources.METADATADISCOVERY || snapshot.Provenance.Revision != hex.EncodeToString(sum[:]) || snapshot.Provenance.ObservedAt.Before(before) || snapshot.Provenance.ObservedAt.After(after) || !snapshot.Provenance.ExpiresAt.IsZero() {
		t.Fatalf("provenance = %+v", snapshot.Provenance)
	}
	serialized := fmt.Sprintf("%+v", snapshot)
	if strings.Contains(serialized, "PRIVATE-CANARY") || strings.Contains(serialized, "not-serving-proof") || strings.Contains(serialized, "unknown_secret_field") {
		t.Error("snapshot retained non-allowlisted data")
	}
	copySnapshot := snapshot.Clone()
	copySnapshot.Rows[0].SupportedProtocols[0] = llm.ModelProtocols.PROTOCOLUNKNOWN
	copySnapshot.Rows[0].RequestedModel = "changed"
	if reflect.DeepEqual(snapshot, copySnapshot) || snapshot.Rows[0].SupportedProtocols[0] != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Error("Clone aliases nested collections")
	}
}

func TestModelInfoMalformedBoundary(t *testing.T) {
	t.Parallel()
	validRow := `{"model_name":"alias","litellm_params":{"model":"upstream"},"model_info":{"id":"dep"}}`
	for _, test := range []struct{ name, body string }{
		{"missing data", `{}`}, {"null envelope", `null`}, {"null data", `{"data":null}`}, {"object data", `{"data":{}}`}, {"null row", `{"data":[null]}`}, {"missing fields", `{"data":[{}]}`},
		{"trailing value", `{"data":[]} {}`}, {"invalid UTF8", "{\"data\":[{\"model_name\":\"\xff\"}]}"}, {"oversize", `{"ignored":"` + strings.Repeat("a", 8<<20) + `","data":[]}`},
		{"empty alias", `{"data":[` + strings.Replace(validRow, `"alias"`, `""`, 1) + `]}`},
		{"alias low surrogate", `{"data":[` + strings.Replace(validRow, `"alias"`, `"\udc00"`, 1) + `]}`},
		{"upstream high surrogate", `{"data":[` + strings.Replace(validRow, `"upstream"`, `"\ud800"`, 1) + `]}`},
		{"deployment surrogate", `{"data":[` + strings.Replace(validRow, `"dep"`, `"\ud800x"`, 1) + `]}`},
		{"deployment control", `{"data":[` + strings.Replace(validRow, `"dep"`, `"a\n"`, 1) + `]}`},
		{"missing deployment", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":null`, 1) + `]}`},
		{"wrong params type", `{"data":[` + strings.Replace(validRow, `{"model":"upstream"}`, `"secret"`, 1) + `]}`},
		{"base surrogate", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","base_model":"\udc00"`, 1) + `]}`},
		{"pricing provider control", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","pricing_provider":"x\u0000"`, 1) + `]}`},
		{"negative rate", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","input_cost_per_token":-1`, 1) + `]}`},
		{"overflow rate", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","cache_creation_input_token_cost":1e308`, 1) + `]}`},
		{"nonfinite rate", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","output_cost_per_token":NaN`, 1) + `]}`},
		{"string rate", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","output_cost_per_token":"0"`, 1) + `]}`},
		{"underflow ceiling", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","max_input_tokens":1e-999`, 1) + `]}`},
		{"underflow rate", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","input_cost_per_token":1e-999`, 1) + `]}`},
		{"fractional ceiling", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","max_input_tokens":8192.000000000000001`, 1) + `]}`},
		{"wrong endpoints type", `{"data":[` + strings.Replace(validRow, `"id":"dep"`, `"id":"dep","supported_endpoints":[null]`, 1) + `]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := &trackedBody{Reader: strings.NewReader(test.body)}
			p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header), Request: r}, nil
			})}))
			snapshot, err := p.ModelInfo(t.Context())
			if !errors.Is(err, litellm.ErrModelInfoResponse) || snapshot.Rows != nil {
				t.Fatalf("snapshot = %+v, error = %v", snapshot, err)
			}
			if !body.closed.Load() {
				t.Error("body not closed")
			}
		})
	}
}

func TestModelInfoOptionalAndRepeatedIdentities(t *testing.T) {
	t.Parallel()
	body := `{"data":[{"model_name":"a\ud83d\ude00","litellm_params":{"model":"provider/opaque/path"},"model_info":{"id":"dep-1","max_tokens":9000,"max_input_tokens":null,"max_output_tokens":0,"base_model":null,"pricing_provider":null}},{"model_name":"a😀","litellm_params":{"model":"other"},"model_info":{"id":"dep-2","max_input_tokens":-1}},{"model_name":"a😀","litellm_params":{"model":"other"},"model_info":{"id":"dep-2"}}]}`
	snapshot, err := infoProvider(body).ModelInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 3 || snapshot.Rows[0].RequestedModel != snapshot.Rows[1].RequestedModel || snapshot.Rows[0].Limits != (llm.ModelLimits{}) || snapshot.Rows[1].Limits != (llm.ModelLimits{}) || snapshot.Rows[0].SupportedProtocols != nil {
		t.Fatalf("rows = %+v", snapshot.Rows)
	}
	empty, err := infoProvider(`{"data":[]}`).ModelInfo(t.Context())
	if err != nil || empty.Rows == nil || len(empty.Rows) != 0 {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
}

func TestModelInfoPricingOverridePresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, field string
		want        litellm.ModelInfoPricingOverrides
	}{
		{"absent", "", litellm.ModelInfoPricingOverrides{}},
		{"null", `,"pricing_overrides":null`, litellm.ModelInfoPricingOverrides{}},
		{"empty", `,"pricing_overrides":[]`, litellm.ModelInfoPricingOverrides{Known: true}},
		{"unknown only", `,"pricing_overrides":["secret"]`, litellm.ModelInfoPricingOverrides{Known: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := `{"data":[{"model_name":"alias","litellm_params":{"model":"upstream"},"model_info":{"id":"dep"` + test.field + `}}]}`
			snapshot, err := infoProvider(body).ModelInfo(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := snapshot.Rows[0].PricingOverrides; got != test.want {
				t.Fatalf("overrides = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestModelInfoStatusAndSanitization(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status int
		err    error
	}{
		{http.StatusForbidden, litellm.ErrModelInfoUnavailable}, {http.StatusNotFound, litellm.ErrModelInfoUnavailable}, {http.StatusUnauthorized, llm.ErrInvalidAPIKey}, {http.StatusTooManyRequests, llm.ErrRateLimitExceeded}, {http.StatusServiceUnavailable, llm.ErrProviderUnavailable},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			body := &trackedBody{Reader: strings.NewReader("PRIVATE-CANARY")}
			p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: body, Header: make(http.Header), Request: r}, nil
			})}))
			_, err := p.ModelInfo(t.Context())
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
			if errors.Is(err, litellm.ErrModelInfoUnavailable) && (errors.Is(err, llm.ErrInvalidAPIKey) || errors.Is(err, litellm.ErrModelsResponse)) {
				t.Error("unavailable conflated with auth or model list")
			}
			if strings.Contains(err.Error(), "PRIVATE-CANARY") || strings.Contains(err.Error(), "ordinary-test-key") {
				t.Error("private diagnostic")
			}
			if !body.closed.Load() {
				t.Error("error body not closed")
			}
		})
	}
	transportErr := errors.New("PRIVATE-CANARY")
	p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, transportErr })}))
	_, err := p.ModelInfo(t.Context())
	if !errors.Is(err, litellm.ErrTransport) || errors.Is(err, transportErr) || strings.Contains(err.Error(), "PRIVATE-CANARY") {
		t.Fatalf("transport = %v", err)
	}
}

func TestModelInfoCancellation(t *testing.T) {
	t.Parallel()
	cause := errors.New("stop enrichment")
	ctx, cancel := context.WithCancelCause(t.Context())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[`)
		w.(http.Flusher).Flush()
		cancel(cause)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL))
	_, err := p.ModelInfo(ctx)
	if !errors.Is(err, cause) {
		t.Fatalf("cancellation = %v", err)
	}
	_, err = p.ModelInfo(ctx)
	if !errors.Is(err, cause) {
		t.Fatalf("pre-cancellation = %v", err)
	}
}

func TestModelInfoReadErrorIsSanitized(t *testing.T) {
	t.Parallel()
	body := &modelInfoErrorBody{}
	p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
	})}))
	_, err := p.ModelInfo(t.Context())
	if !errors.Is(err, litellm.ErrModelInfoResponse) || strings.Contains(err.Error(), "PRIVATE-CANARY") || !body.closed {
		t.Fatalf("read error = %v, closed = %t", err, body.closed)
	}
}

type modelInfoErrorBody struct{ closed bool }

func (*modelInfoErrorBody) Read([]byte) (int, error) { return 0, errors.New("PRIVATE-CANARY") }
func (b *modelInfoErrorBody) Close() error           { b.closed = true; return nil }

func TestModelInfoTaggedSingleModelEnvelope(t *testing.T) {
	t.Parallel()
	// v1.109.0 model_info_v1's user_model path returns a deployment object
	// rather than the usual array. Its identities must pass the same boundary.
	snapshot, err := infoProvider(`{"data":{"model_name":"*","litellm_params":{"model":"openai/opaque"},"model_info":{"id":"deployment"}}}`).ModelInfo(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Rows) != 1 || snapshot.Rows[0].RequestedModel != "*" || snapshot.Rows[0].UpstreamModel != "openai/opaque" || snapshot.Rows[0].Deployment != "deployment" {
		t.Fatalf("single deployment = %+v", snapshot.Rows)
	}
}
