package program

import (
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func (r *runner) normalizeOutput(raw any) (any, error) {
	limit := r.source.limits.MaxOutputBytes
	if r.source.outputRetainer == nil {
		return normalizeJSON(raw, limit)
	}
	// fromStarlark already validates depth, keys, types and finite numbers.
	// Keep exact JSON bytes, including string quotes/escapes, not Result.String.
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if len(data) <= limit {
		return decodeJSON(data)
	}
	receipt, err := r.source.outputRetainer.Retain(r.ctx, data)
	if err != nil {
		return nil, r.setScriptError(tools.Budget(op, fmt.Sprintf("output exceeds %d bytes; output not retained; reduce the emitted result", limit)))
	}
	// Starlark wraps builtin errors; preserve the semantic budget kind through
	// scriptErr. No successful emission or completion admission is recorded.
	return nil, r.setScriptError(tools.Budget(op, fmt.Sprintf(
		"output exceeds %d bytes; retained %d JSON bytes. Read with program_output_read(receipt=%q, offset=0); follow next_offset. Do not rerun the program. Receipt expires after 15 minutes or conversation replacement.",
		limit, len(data), receipt)))
}
