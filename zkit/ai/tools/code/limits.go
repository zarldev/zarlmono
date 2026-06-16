package code

import "github.com/zarldev/zarlmono/zkit/zenv"

// Per-tool argument-size caps. Historically these existed because
// older llama.cpp builds dropped characters inside long streaming
// tool-call JSON arguments, surfacing as "Failed to parse tool call
// arguments" — the cap rejected oversized content with a clear
// chunked-write hint so the model recovered on the next call instead
// of looping on the same JSON-parse error.
//
// Modern llama-server builds handle large args reliably, so the
// defaults are now generous: 256KB for write/append (enough for any
// realistic source file in one shot), 64KB per edit arg. Override
// at startup via the env knobs below; the values are read once at
// package init.
//
//	CODE_WRITE_MAX_BYTES   default 262144  write/content
//	CODE_APPEND_MAX_BYTES  default 262144  write_append/content
//	CODE_EDIT_MAX_BYTES    default 65536   edit/old_string and edit/new_string
//
// Set to 0 to remove the cap entirely.
var (
	maxWriteContentBytes  = zenv.Int("CODE_WRITE_MAX_BYTES", 256*1024)
	maxAppendContentBytes = zenv.Int("CODE_APPEND_MAX_BYTES", 256*1024)
	maxEditArgBytes       = zenv.Int("CODE_EDIT_MAX_BYTES", 64*1024)
)
