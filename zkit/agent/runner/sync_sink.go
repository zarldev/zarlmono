package runner

import (
	"context"
	"sync"
)

// SyncSink wraps an EventSink with a mutex so the wrapped sink is
// called from exactly one goroutine at a time. Use it when your sink
// is not already safe for concurrent calls (e.g. it appends to a
// slice or updates a map) — the runner fires events from multiple
// goroutines under WithToolConcurrency and across concurrent Runs, so
// an unsynchronised sink races. See [EventSink]'s concurrency contract.
// It forwards each context unchanged, including when already cancelled.
//
// The wrapped sink must be non-nil. NewSyncSink panics if sink is nil.
type SyncSink struct {
	mu   sync.Mutex
	sink EventSink
}

// NewSyncSink wraps sink so every event method serialises behind one
// mutex. sink must be non-nil — a nil sink is a programming error, not
// a no-op, so NewSyncSink panics rather than deferring the nil
// dereference to the first event.
func NewSyncSink(sink EventSink) *SyncSink {
	if sink == nil {
		panic("runner.NewSyncSink: sink is nil")
	}
	return &SyncSink{sink: sink}
}

// OnContent forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnContent(ctx context.Context, e Content) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnContent(ctx, e)
}

// OnThinking forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnThinking(ctx context.Context, e Thinking) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnThinking(ctx, e)
}

// OnToolStarted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnToolStarted(ctx context.Context, e ToolStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnToolStarted(ctx, e)
}

// OnToolCompleted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnToolCompleted(ctx context.Context, e ToolCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnToolCompleted(ctx, e)
}

// OnToolFailed forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnToolFailed(ctx context.Context, e ToolFailed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnToolFailed(ctx, e)
}

// OnConversationStarted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnConversationStarted(ctx context.Context, e ConversationStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnConversationStarted(ctx, e)
}

// OnConversationEnded forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnConversationEnded(ctx context.Context, e ConversationEnded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnConversationEnded(ctx, e)
}

// OnIterationCompleted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnIterationCompleted(ctx context.Context, e IterationCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnIterationCompleted(ctx, e)
}

// OnSteerInjected forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnSteerInjected(ctx context.Context, e SteerInjected) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnSteerInjected(ctx, e)
}

// OnCompactionApplied forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnCompactionApplied(ctx context.Context, e CompactionApplied) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnCompactionApplied(ctx, e)
}

// OnDiagnostic forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnDiagnostic(ctx context.Context, e Diagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnDiagnostic(ctx, e)
}

func (s *SyncSink) OnWorkspaceWaitStarted(ctx context.Context, e WorkspaceWaitStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnWorkspaceWaitStarted(ctx, e)
}

func (s *SyncSink) OnWorkspaceWaitEnded(ctx context.Context, e WorkspaceWaitEnded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnWorkspaceWaitEnded(ctx, e)
}

// OnWaitingForInputs forwards live wait state under the sink mutex.
func (s *SyncSink) OnWaitingForInputs(ctx context.Context, e WaitingForInputs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnWaitingForInputs(ctx, e)
}

// OnInputsAdmitted forwards successful admission under the sink mutex.
func (s *SyncSink) OnInputsAdmitted(ctx context.Context, e InputsAdmitted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnInputsAdmitted(ctx, e)
}
