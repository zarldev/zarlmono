// Package compact provides conversation compaction strategies for agent runs.
//
// It includes structural trimming, tiered pressure handling, summary-based
// compaction, and orchestration helpers used by the runner and interactive
// shells when token pressure grows. Prompt behavior is shared above provider
// adapters: LLM-driven compactors use provider-neutral prompts and messages.
//
// Displayable llm.Message.ReasoningContent participates in ordinary text sizing
// and summarization. Provider-native llm.Message.ContinuationItems remain
// opaque: compactors count their bytes, clone retained items, and never interpret
// or truncate payloads. Summary/collapse strategies discard them with an owning
// message; Tiered Phase 3 additionally defines an explicit native replay boundary
// by dropping whole items from older retained messages while preserving projected
// content and paired tool history. Recent turns retain exact replay.
package compact
