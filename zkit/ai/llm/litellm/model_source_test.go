package litellm_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

func TestModelSourceLastGoodAndExpiry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		var status atomic.Int64
		var malformed atomic.Bool
		status.Store(http.StatusOK)
		p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			body := modelInfoFixture
			if malformed.Load() {
				body = `{"data":[null]}`
			}
			return &http.Response{StatusCode: int(status.Load()), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}))
		source := litellm.NewModelSource(p, time.Minute)
		if calls.Load() != 0 {
			t.Fatal("constructor did network I/O")
		}
		if _, err := source.Snapshot(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
			t.Fatalf("empty = %v", err)
		}
		refreshed, err := source.Refresh(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		expiry := refreshed.Provenance.ExpiresAt
		if !expiry.Equal(refreshed.Provenance.ObservedAt.Add(time.Minute)) {
			t.Fatal("TTL not based on observation")
		}
		refreshed.Rows[0].SupportedProtocols[0] = llm.ModelProtocols.PROTOCOLUNKNOWN
		refreshed.Rows[0].Deployment = "mutation"
		cached, err := source.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if cached.Rows[0].Deployment != "deployment-one" || cached.Rows[0].SupportedProtocols[0] != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
			t.Fatal("Refresh escaped mutable cache")
		}
		time.Sleep(time.Minute)
		cached, err = source.Snapshot()
		if err != nil || cached.Provenance.ExpiresAt.After(time.Now()) || calls.Load() != 1 {
			t.Fatalf("expiry lookup = %+v, %v; calls=%d", cached.Provenance, err, calls.Load())
		}
		status.Store(http.StatusForbidden)
		if _, err = source.Refresh(t.Context()); !errors.Is(err, litellm.ErrModelInfoUnavailable) {
			t.Fatalf("denied refresh = %v", err)
		}
		after, err := source.Snapshot()
		if err != nil || !after.Provenance.ExpiresAt.Equal(expiry) {
			t.Fatalf("lost or extended last good = %+v, %v", after, err)
		}
		status.Store(http.StatusServiceUnavailable)
		if _, err = source.Refresh(t.Context()); !errors.Is(err, llm.ErrProviderUnavailable) {
			t.Fatalf("failed refresh = %v", err)
		}
		status.Store(http.StatusOK)
		malformed.Store(true)
		if _, err = source.Refresh(t.Context()); !errors.Is(err, litellm.ErrModelInfoResponse) {
			t.Fatalf("malformed refresh = %v", err)
		}
		after, err = source.Snapshot()
		if err != nil || !after.Provenance.ExpiresAt.Equal(expiry) {
			t.Fatalf("malformed response extended cache: %+v, %v", after.Provenance, err)
		}
		malformed.Store(false)
		next, err := source.Refresh(t.Context())
		if err != nil || !next.Provenance.ExpiresAt.After(expiry) {
			t.Fatalf("successful refresh = %+v, %v", next, err)
		}
		if calls.Load() != 5 {
			t.Fatalf("requests = %d", calls.Load())
		}
	})
}

func TestModelSourceTenantIsolationAndConcurrentSnapshots(t *testing.T) {
	t.Parallel()
	first := litellm.NewModelSource(infoProvider(modelInfoFixture), time.Minute)
	other := litellm.NewModelSource(infoProvider(`{"data":[]}`), time.Minute)
	if _, err := first.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Snapshot(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
		t.Fatalf("other scope cache = %v", err)
	}
	if _, err := other.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 10 {
				snapshot, err := first.Snapshot()
				if err != nil {
					t.Error(err)
					return
				}
				snapshot.Rows[0].RequestedModel = "changed"
				snapshot.Rows[0].SupportedProtocols[0] = llm.ModelProtocols.PROTOCOLUNKNOWN
			}
		})
	}
	wg.Go(func() {
		for range 10 {
			if _, err := first.Refresh(t.Context()); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
	second, err := other.Snapshot()
	if err != nil || len(second.Rows) != 0 {
		t.Fatalf("other scope = %+v, %v", second, err)
	}
}

func TestModelSourceRefreshWaitCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int64
		p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, context.Cause(r.Context())
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(modelInfoFixture)), Request: r}, nil
		})}))
		source := litellm.NewModelSource(p, time.Minute)
		done := make(chan error, 1)
		go func() { _, err := source.Refresh(t.Context()); done <- err }()
		<-started
		cause := errors.New("cancel queued refresh")
		ctx, cancel := context.WithCancelCause(t.Context())
		queued := make(chan error, 1)
		go func() { _, err := source.Refresh(ctx); queued <- err }()
		synctest.Wait()
		cancel(cause)
		err := <-queued
		close(release)
		firstErr := <-done
		if firstErr != nil {
			t.Fatal(firstErr)
		}
		if !errors.Is(err, cause) || calls.Load() != 1 {
			t.Fatalf("queued cancellation = %v, calls = %d", err, calls.Load())
		}
	})
}

func TestModelSourceNonpositiveTTLAndFirstFailure(t *testing.T) {
	t.Parallel()
	for _, ttl := range []time.Duration{0, -time.Minute} {
		source := litellm.NewModelSource(infoProvider(modelInfoFixture), ttl)
		snapshot, err := source.Refresh(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Provenance.ExpiresAt.After(snapshot.Provenance.ObservedAt) {
			t.Fatal("nonpositive TTL became fresh")
		}
	}
	source := litellm.NewModelSource(infoProvider(`{"data":[null]}`), time.Minute)
	if _, err := source.Refresh(t.Context()); !errors.Is(err, litellm.ErrModelInfoResponse) {
		t.Fatalf("first refresh = %v", err)
	}
	if _, err := source.Snapshot(); !errors.Is(err, litellm.ErrModelInfoNotCached) {
		t.Fatalf("invalid initial snapshot retained: %v", err)
	}
}
