package runner

import "sync"

// SyncSink wraps an EventSink with a mutex so the wrapped sink is
// called from exactly one goroutine at a time. Use it when your sink
// is not already safe for concurrent calls (e.g. it appends to a
// slice or updates a map) — the runner fires events from multiple
// goroutines under WithToolConcurrency and across concurrent Runs, so
// an unsynchronised sink races. See [EventSink]'s concurrency contract.
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
func (s *SyncSink) OnContent(e Content) { s.mu.Lock(); defer s.mu.Unlock(); s.sink.OnContent(e) }

// OnThinking forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnThinking(e Thinking) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnThinking(e)
}

// OnToolStarted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnToolStarted(e ToolStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnToolStarted(e)
}

// OnToolCompleted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnToolCompleted(e ToolCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnToolCompleted(e)
}

// OnToolFailed forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnToolFailed(e ToolFailed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnToolFailed(e)
}

// OnConversationStarted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnConversationStarted(e ConversationStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnConversationStarted(e)
}

// OnConversationEnded forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnConversationEnded(e ConversationEnded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnConversationEnded(e)
}

// OnIterationCompleted forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnIterationCompleted(e IterationCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnIterationCompleted(e)
}

// OnSteerInjected forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnSteerInjected(e SteerInjected) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnSteerInjected(e)
}

// OnCompactionApplied forwards to the wrapped sink under the mutex.
func (s *SyncSink) OnCompactionApplied(e CompactionApplied) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink.OnCompactionApplied(e)
}
