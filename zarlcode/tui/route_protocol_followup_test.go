package tui_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// routeFollowupProvider holds a real streaming turn open until the test releases
// it. Cancellation also unblocks the stream, so cleanup can always join dispatch.
type routeFollowupProvider struct {
	started chan struct{}
	release chan struct{}
}

func (*routeFollowupProvider) Name() string { return "litellm" }

func (p *routeFollowupProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Content: "controlled partial answer"}, nil) {
			return
		}
		close(p.started)
		select {
		case <-ctx.Done():
			yield(llm.CompletionChunk{}, ctx.Err())
		case <-p.release:
			yield(llm.CompletionChunk{Content: " and settled answer"}, nil)
		}
	}
}

func TestRouteProtocolEditorActiveTurnKeepsRuntimeUntilSettledRetry(t *testing.T) {
	s, ui, original, primary := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	setPlanRoute(t, s, primary.Name, "plan/alias")
	step(t, ui, routeToggleMode(t, ui)())
	step(t, ui, routeManualBuild(t, ui, primary.Name, "manual/active alias")())
	spec := original.RunTarget().Spec
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	var mu sync.Mutex
	var events []tea.Msg
	sink := teasink.New(func(msg tea.Msg) {
		mu.Lock()
		events = append(events, msg)
		mu.Unlock()
	})
	t.Cleanup(sink.Close)
	drain := func() []tea.Msg {
		sink.Drain()
		mu.Lock()
		defer mu.Unlock()
		batch := events
		events = nil
		return batch
	}
	provider := &routeFollowupProvider{started: make(chan struct{}), release: make(chan struct{})}
	live := engine.NewLiveRunner(provider, ws, spec.Model, engine.WithLiveSink(sink))
	live.SetProviderSpec(provider, spec)
	live.SetPlanMode(true)
	testCtx := t.Context()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ui.SetLiveRunner(live)
	ui.SetLiveEventSink(sink)
	ui.SetStartupReady(true)
	ui.SetApplicationContext(t.Context())

	cmd := ui.Submit("controlled active prompt")
	if cmd == nil {
		t.Fatalf("turn not dispatched: %s", ui.ToastText())
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runBeforeCommands(cmd)
	}()
	var release sync.Once
	t.Cleanup(func() {
		live.CancelTurn()
		release.Do(func() { close(provider.release) })
		<-done
	})
	select {
	case <-provider.started:
	case <-done:
		t.Fatal("dispatch returned without entering controlled provider")
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	for _, msg := range drain() {
		step(t, ui, msg)
	}
	step(t, ui, tea.PasteMsg{Content: "retained unsent draft"})
	identity := ui.SessionIdentity()
	if identity == "" {
		t.Fatal("active turn has no conversation identity")
	}
	thread := ui.CanonicalThread()
	transcript := ui.RenderTranscript(150, 30)
	if !strings.Contains(ansi.Strip(strings.Join(transcript, "\n")), "controlled partial answer") {
		t.Fatal("active streaming content was not applied")
	}
	mode := live.AppliedMode()
	before := live.RunTarget()
	preferences, err := s.Store.EffectiveSettings(t.Context(), s.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	durable, err := s.Store.GetSessionResumeState(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}

	saveResponsesPolicy(t, ui)
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLRESPONSES)
	if live.RunTarget().Provider != provider {
		t.Fatal("policy save replaced an executing provider")
	}
	closeOpenSettings(t, ui)
	requireRouteView(t, ui, "Policy saved; current provider not refreshed:")
	if got := live.RunTarget(); got.Provider != provider || got.Spec != before.Spec || got.Window != before.Window {
		t.Fatalf("busy close changed active runtime: %+v", got)
	}
	if ui.SessionIdentity() != identity || ui.ComposerText() != "retained unsent draft" || live.AppliedMode() != mode {
		t.Fatal("busy close changed conversation, draft or workflow mode")
	}
	if !reflect.DeepEqual(thread.Entries(), ui.CanonicalThread().Entries()) {
		t.Fatal("busy close changed conversation entries")
	}
	if diff := cmp.Diff(transcript, ui.RenderTranscript(150, 30)); diff != "" {
		t.Fatalf("busy close changed streaming transcript (-before +after):\n%s", diff)
	}
	requireRouteFollowupPreferences(t, s, preferences)
	afterBusy, err := s.Store.GetSessionResumeState(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(durable, afterBusy) {
		t.Fatal("busy policy edit changed the durable conversation")
	}
	select {
	case <-done:
		t.Fatal("settings close ended the controlled active turn")
	default:
	}
	// The pending failure must remain visible on reopening the real editor.
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireRouteView(t, ui, "Saved policy: responses")
	requireRouteView(t, ui, "Policy saved; current provider not refreshed:")
	closeRouteEditor(t, ui)

	release.Do(func() { close(provider.release) })
	<-done
	messages := drain()
	if len(messages) == 0 {
		t.Fatal("controlled turn did not publish settlement")
	}
	for _, msg := range messages[:len(messages)-1] {
		step(t, ui, msg)
	}
	_, save := ui.Update(messages[len(messages)-1])
	if save == nil {
		t.Fatal("settlement did not request durable save")
	}
	runSettingsCloseCmd(t, ui, save)
	if live.RunTarget().Provider != provider {
		t.Fatal("settlement refreshed policy without the explicit close retry")
	}
	settled := live.ContextSnapshot()
	closeSettings(t, ui)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLRESPONSES)
	if ui.SessionIdentity() != identity || ui.ComposerText() != "retained unsent draft" || live.AppliedMode() != mode {
		t.Fatal("retry changed conversation, draft or override intent")
	}
	if !reflect.DeepEqual(settled, live.ContextSnapshot()) {
		t.Fatal("refresh changed settled model context")
	}
	requireRouteFollowupPreferences(t, s, preferences)
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLRESPONSES)
	// The helper established a manual override. An unchanged settings close
	// must not turn that override back into mode routing after refresh.
	refreshed := live.RunTarget().Provider
	closeSettings(t, ui)
	if live.RunTarget().Provider != refreshed {
		t.Fatal("refresh lost override intent or left a pending rebuild")
	}
}

func TestRouteProtocolEditorUnreadableBaselineSuccessfulSaveRefreshesActivePair(t *testing.T) {
	s, ui, live, spec := newRouteEditorUI(t, llm.ModelProtocols.PROTOCOLUNKNOWN)
	if err := s.Registry.SetRouteProtocol(t.Context(), spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLRESPONSES); err != nil {
		t.Fatal(err)
	}
	step(t, ui, routeManualBuild(t, ui, spec.Name, spec.Model)())
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLRESPONSES)
	before := live.RunTarget().Provider
	preferences, err := s.Store.EffectiveSettings(t.Context(), s.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(routeEditorStore{
		RouteProtocolStore: s.Store, readErr: errRouteEditorStorage,
	}))
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireRouteView(t, ui, "Saved policy unavailable:")
	// Saving Chat is deliberate: treating the unknown baseline as the default
	// Chat would incorrectly suppress this real Responses-to-Chat transition.
	saveRouteChoice(t, ui, 0, 1)
	requireRouteView(t, ui, "Chat Completions [saved]")
	requireRouteView(t, ui, "Policy saved;")
	if live.RunTarget().Provider != before {
		t.Fatal("save refreshed provider before settings close")
	}
	// Recover reads only after the successful write; use the actual durable
	// store for both the assertion and subsequent provider construction.
	s.Registry = backends.NewRegistry(backends.WithProviderDefinitions(routeEditorDefinitions()), backends.WithRouteProtocolStore(s.Store))
	requireRoutePolicy(t, s, spec.Name, spec.Model, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS)
	closeRouteEditor(t, ui)
	requireRouteRuntime(t, live, spec, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS)
	if live.RunTarget().Provider == before {
		t.Fatal("unknown baseline suppressed the active-pair refresh")
	}
	requireRouteFollowupPreferences(t, s, preferences)
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireRouteView(t, ui, "Saved policy: chat_completions")
	closeRouteEditor(t, ui)
}

func requireRouteFollowupPreferences(t *testing.T, s *engine.Settings, want map[string]string) {
	t.Helper()
	got, err := s.Store.EffectiveSettings(t.Context(), s.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("protocol edit changed preferences (-before +after):\n%s", diff)
	}
}
