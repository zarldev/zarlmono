// Package runner implements the core agent loop.
//
// A Runner renders prompts, streams model output, dispatches tool calls,
// publishes structured events, handles compaction/truncation, and supports
// interactive steering. The package keeps its contracts small so consumers can
// provide their own clients, tool sources, sinks, and prompt sources.
package runner
