package litellm_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestRedirectsNeverTransferCredentials(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		for _, crossOrigin := range []bool{true, false} {
			name := fixture.protocol.String() + "/same-origin"
			if crossOrigin {
				name = fixture.protocol.String() + "/cross-origin"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var destinationRequests, sourceRequests, clientRedirects atomic.Int64
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					destinationRequests.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(destination.Close)
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sourceRequests.Add(1)
					if r.URL.Path == "/redirected" {
						t.Error("same-origin redirect followed")
					}
					assertAuth(t, r, fixture.protocol)
					target := "/redirected"
					if crossOrigin {
						target = destination.URL + "/redirected"
					}
					w.Header().Set("Location", target)
					w.WriteHeader(http.StatusTemporaryRedirect)
				}))
				t.Cleanup(source.Close)
				client := source.Client()
				client.Timeout = time.Minute
				policy := func(_ *http.Request, _ []*http.Request) error { clientRedirects.Add(1); return nil }
				client.CheckRedirect = policy
				originalTransport := client.Transport
				p := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(source.URL), litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(client))
				var terminal error
				for _, err := range p.Complete(t.Context(), providertest.SimpleRequest("hi")) {
					if err != nil {
						terminal = err
					}
				}
				var status *litellm.HTTPError
				if !errors.As(terminal, &status) || status.StatusCode != http.StatusTemporaryRedirect {
					t.Fatalf("redirect error = %v", terminal)
				}
				if sourceRequests.Load() < 1 || destinationRequests.Load() != 0 || clientRedirects.Load() != 0 {
					t.Fatalf("redirect followed: source=%d destination=%d caller callback=%d", sourceRequests.Load(), destinationRequests.Load(), clientRedirects.Load())
				}
				if client.Timeout != time.Minute || client.Transport != originalTransport || reflect.ValueOf(client.CheckRedirect).Pointer() != reflect.ValueOf(policy).Pointer() {
					t.Error("caller client mutated")
				}
			})
		}
	}
}

func TestModelsRedirectAndNilTransport(t *testing.T) {
	t.Parallel()
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.Header().Set("Location", "/other")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: time.Minute}
	p := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL), litellm.WithHTTPClient(client))
	_, err := p.Models(t.Context())
	var status *litellm.HTTPError
	if !errors.As(err, &status) || status.StatusCode != http.StatusFound {
		t.Fatalf("redirect error = %v", err)
	}
	if count.Load() != 1 || client.Transport != nil || client.CheckRedirect != nil || client.Timeout != time.Minute {
		t.Fatal("redirect followed or client mutated")
	}
}

func TestEarlyStopClosesBody(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			fixture.serve(recorder, true, true)
			body := &trackedBody{Reader: strings.NewReader(recorder.Body.String())}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: recorder.Header().Clone(), Body: body, Request: r}, nil
			})}
			p := litellm.NewProvider("ordinary-test-key", litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(client))
			var yields int
			p.Complete(t.Context(), providertest.SimpleRequest("hi"))(func(_ llm.CompletionChunk, err error) bool {
				yields++
				if err != nil {
					t.Errorf("early stop = %v", err)
				}
				return false
			})
			if yields != 1 || !body.closed.Load() {
				t.Fatalf("early stop yields = %d, closed = %t", yields, body.closed.Load())
			}
		})
	}
}

func TestOnlyConfiguredCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-openai-secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-anthropic-secret")
	t.Setenv("GOOGLE_API_KEY", "ambient-google-secret")
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			p := litellm.NewProvider("ordinary-test-key", litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				assertAuth(t, r, fixture.protocol)
				recorder := httptest.NewRecorder()
				fixture.serve(recorder, true, true)
				return &http.Response{StatusCode: http.StatusOK, Header: recorder.Header().Clone(), Body: io.NopCloser(strings.NewReader(recorder.Body.String())), Request: r}, nil
			})}))
			collect(t, p.Complete(t.Context(), providertest.SimpleRequest("hi")))
		})
	}
}
