package program

import (
	"context"
	"crypto/rand"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	outputObjectBytes  = 1 << 20
	outputTotalBytes   = 8 << 20
	outputEntries      = 32
	outputTTL          = 15 * time.Minute
	outputPageBytes    = 8 << 10
	outputMaxPageBytes = 16 << 10
)

// ToolNameOutputRead is the direct tool for paging retained program JSON.
const ToolNameOutputRead tools.ToolName = "program_output_read"

type retainedOutput struct {
	receipt string
	json    string
	expires time.Time
}

// OutputStore owns one conversation activation's ephemeral serialized outputs.
// It retains at most 1 MiB per output, 8 MiB total and 32 outputs for 15 minutes,
// evicting oldest insertions first. Expiry is checked on operations, not by a
// background worker. Close revokes this capability permanently; never reuse a
// store for a new conversation. Retention is not durable or secure erasure.
// The store must not be copied after use.
type OutputStore struct {
	mu      sync.Mutex
	entries []retainedOutput
	bytes   int
	closed  bool
}

// NewOutputStore creates an empty activation store without starting workers.
func NewOutputStore() *OutputStore { return &OutputStore{} }

// Retain copies validated serialized JSON and returns an opaque receipt.
// The caller owns validation; quota checks do not bound serialization allocation.
// A closed store rejects all insertions, including those from stale sources.
func (s *OutputStore) Retain(ctx context.Context, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.closed {
		return "", outputUnavailable()
	}
	if len(data) > outputObjectBytes {
		return "", tools.Budget(op, "output exceeds 1 MiB retention limit")
	}
	now := time.Now()
	s.expire(now)
	for len(s.entries) >= outputEntries || s.bytes+len(data) > outputTotalBytes {
		s.bytes -= len(s.entries[0].json)
		s.entries = slices.Delete(s.entries, 0, 1)
	}
	receipt := rand.Text()
	s.entries = append(s.entries, retainedOutput{receipt: receipt, json: string(data), expires: now.Add(outputTTL)})
	s.bytes += len(data)
	return receipt, nil
}

// Close revokes all receipts and releases retained strings. It is idempotent;
// previously copied pages remain owned by their callers.
func (s *OutputStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.entries)
	s.entries = nil
	s.bytes = 0
	s.closed = true
}

func (s *OutputStore) expire(now time.Time) {
	s.entries = slices.DeleteFunc(s.entries, func(entry retainedOutput) bool {
		if !now.Before(entry.expires) {
			s.bytes -= len(entry.json)
			return true
		}
		return false
	})
}

// OutputReadArgs selects a UTF-8-boundary page of the exact retained JSON bytes.
type OutputReadArgs struct {
	Receipt string `json:"receipt" doc:"Opaque receipt from program overflow."`
	Offset  int    `json:"offset,omitempty" doc:"Byte offset; use next_offset from the previous page. Default 0."`
	Limit   int    `json:"limit,omitempty" doc:"Page bytes: 4–16384; default 8192. Never reruns the program."`
}

// OutputPage contains a copied JSON fragment, not a separately parsed JSON value.
// Concatenating Content in offset order reconstructs the original serialized JSON.
type OutputPage struct {
	Content    string `json:"content"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	Done       bool   `json:"done"`
}

// Read returns a copied bounded page. Unavailable, expired, evicted and revoked
// receipts have the same not-found error, including receipts from other stores.
func (s *OutputStore) Read(ctx context.Context, args OutputReadArgs) (OutputPage, error) {
	if args.Offset < 0 || args.Limit < 0 || args.Limit > outputMaxPageBytes || args.Limit > 0 && args.Limit < utf8.UTFMax {
		return OutputPage{}, tools.Validation(ToolNameOutputRead.String(), "offset must be non-negative; limit must be 0 (default) or 4–16384")
	}
	if args.Limit == 0 {
		args.Limit = outputPageBytes
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return OutputPage{}, tools.Transient(ToolNameOutputRead.String(), err)
	}
	if s.closed {
		return OutputPage{}, outputUnavailable()
	}
	s.expire(time.Now())
	for _, entry := range s.entries {
		if entry.receipt != args.Receipt {
			continue
		}
		if args.Offset > len(entry.json) || args.Offset < len(entry.json) && !utf8.RuneStart(entry.json[args.Offset]) {
			return OutputPage{}, tools.Validation(ToolNameOutputRead.String(), "offset must be a UTF-8 boundary within the output")
		}
		end := min(args.Offset+args.Limit, len(entry.json))
		for end < len(entry.json) && !utf8.RuneStart(entry.json[end]) {
			end--
		}
		return OutputPage{Content: strings.Clone(entry.json[args.Offset:end]), NextOffset: end, TotalBytes: len(entry.json), Done: end == len(entry.json)}, nil
	}
	return OutputPage{}, outputUnavailable()
}

func outputUnavailable() *tools.Error {
	return tools.NotFound(ToolNameOutputRead.String(), "output unavailable")
}

// NewOutputReadTool binds the direct reader to one activation's store. The host
// must guard it like other tools; it has no workspace or admission-reference authority.
func NewOutputReadTool(store *OutputStore) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolNameOutputRead,
		Description: "Read retained program JSON without rerunning work. Memory-only receipts expire after 15 minutes or conversation replacement; pages carry no completion admission. Join content in next_offset order.",
		Parameters:  tools.SchemaFor[OutputReadArgs](),
	}, store.Read)
}
