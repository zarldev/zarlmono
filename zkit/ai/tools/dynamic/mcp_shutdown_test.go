package dynamic_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestMCPRegistryCloseCancelsPendingPolicy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := tools.NewRegistry()
		connections := dynamic.NewMCPRegistry(registry, nil)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		entered := make(chan struct{})
		connections.SetConnectPolicy(dynamic.MCPConnectPolicyFunc(func(ctx context.Context, _ string, _ dynamic.MCPConnSpec) error {
			close(entered)
			<-ctx.Done()
			return context.Cause(ctx)
		}))
		var result *tools.ToolResult
		var connectErr error
		connectDone := make(chan struct{})
		go func() {
			defer close(connectDone)
			result, connectErr = dynamic.NewMCPConnect(connections).Execute(ctx, tools.ToolCall{
				ID: "connect", Arguments: tools.ToolParameters{"name": "pending", "transport": "stdio", "command": "/not-launched"},
			})
		}()
		<-entered
		closeDone := make(chan struct{})
		go func() {
			defer close(closeDone)
			if err := connections.CloseAll(); err != nil {
				t.Errorf("CloseAll: %v", err)
			}
		}()
		synctest.Wait()
		select {
		case <-connectDone:
		default:
			t.Error("CloseAll did not cancel the pending approval")
		}
		cancel() // independent cleanup also releases the pre-fix implementation
		<-connectDone
		<-closeDone
		if err := connections.CloseAll(); err != nil {
			t.Fatalf("repeat CloseAll: %v", err)
		}
		if connectErr != nil || result.Success || !strings.Contains(result.Error, context.Canceled.Error()) {
			t.Fatalf("pending connect = %+v, %v, want cancellation", result, connectErr)
		}
		result, err := dynamic.NewMCPConnect(connections).Execute(t.Context(), tools.ToolCall{
			ID: "late", Arguments: tools.ToolParameters{"name": "late", "transport": "stdio", "command": "/not-launched"},
		})
		if err != nil || result.Success || !strings.Contains(result.Error, "registry is closed") {
			t.Fatalf("late connect = %+v, %v, want closed registry", result, err)
		}
	})
}

func TestMCPRegistryCloseCancelsPendingDiscovery(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(entered) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	connections := dynamic.NewMCPRegistry(tools.NewRegistry(), nil)
	connections.SetConnectPolicy(dynamic.NewMCPAllowlistPolicy(map[string]dynamic.MCPConnSpec{
		"pending": dynamic.NewHTTPMCPConn(server.URL, ""),
	}))
	ctx, cancel := context.WithCancel(t.Context())
	connectDone := make(chan struct{})
	var result *tools.ToolResult
	var connectErr error
	go func() {
		defer close(connectDone)
		result, connectErr = dynamic.NewMCPConnect(connections).Execute(ctx, tools.ToolCall{
			ID: "connect", Arguments: tools.ToolParameters{"name": "pending", "transport": "http", "base_url": server.URL},
		})
	}()
	defer func() { cancel(); <-connectDone; _ = connections.CloseAll() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery did not reach peer")
	}
	closeDone := make(chan struct{})
	go func() { defer close(closeDone); _ = connections.CloseAll() }()
	defer func() { cancel(); <-closeDone }()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("CloseAll did not cancel pending discovery")
	}
	<-connectDone
	if connectErr != nil || result.Success || !strings.Contains(result.Error, context.Canceled.Error()) {
		t.Fatalf("discovery = %+v, %v, want cancellation", result, connectErr)
	}
}

func TestMCPRegistryCloseRevokesCallsBeforePendingJoin(t *testing.T) {
	server := newExposureServer(t, "blocking", true)
	connections, exposure := newExposure(t, server)
	ref := searchExposure(t, exposure, "", 10)[0].Ref
	if result := activateExposure(t, exposure, ref); !result.Success {
		t.Fatalf("activate: %+v", result)
	}
	visible := collectTools(t, exposure.Source())
	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan struct{})
	var callErr error
	go func() {
		defer close(callDone)
		_, callErr = visible[0].Execute(ctx, tools.ToolCall{ID: "blocked", ToolName: "inspect_scene"})
	}()
	defer func() { cancel(); <-callDone }()
	<-server.callStarted

	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	connections.SetConnectPolicy(dynamic.MCPConnectPolicyFunc(func(ctx context.Context, _ string, _ dynamic.MCPConnSpec) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release // model cleanup that must join after live admission is revoked
		return context.Cause(ctx)
	}))
	connectDone := make(chan struct{})
	go func() {
		defer close(connectDone)
		_, _ = dynamic.NewMCPConnect(connections).Execute(ctx, tools.ToolCall{
			ID: "connect", Arguments: tools.ToolParameters{"name": "pending", "transport": "stdio", "command": "/not-launched"},
		})
	}()
	<-entered
	closeDone := make(chan struct{})
	go func() { defer close(closeDone); _ = connections.CloseAll() }()
	defer func() { close(release); cancel(); <-connectDone; <-closeDone }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("pending setup was not canceled")
	}
	select {
	case <-callDone:
		if callErr == nil {
			t.Fatal("live call completed without cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("live call was not canceled before pending cleanup joined")
	}
	if got := collectTools(t, exposure.Source()); len(got) != 0 {
		t.Fatalf("tools still visible during shutdown: %d", len(got))
	}
	if _, err := visible[0].Execute(t.Context(), tools.ToolCall{ID: "late", ToolName: "inspect_scene"}); tools.KindOf(err) != tools.Kinds.NOTFOUND {
		t.Fatalf("old generation = %v, want NOTFOUND", err)
	}
	select {
	case <-closeDone:
		t.Fatal("CloseAll returned before pending setup joined")
	default:
	}
}
