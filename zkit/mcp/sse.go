package mcp

import (
	"bufio"
	"errors"
)

// Allow the field prefix and line ending in addition to a full-sized payload.
const maxSSELineBytes = maxRPCBodyBytes + len("data: \r\n") + 1

// ErrSSEEventTooLarge means an SSE event exceeds its JSON-RPC payload limit:
// 16 MiB for responses, 4 MiB for notifications.
var ErrSSEEventTooLarge = errors.New("mcp: SSE event exceeds payload limit")

func appendSSEData(data []byte, payload string, limit int) ([]byte, error) {
	size := len(data) + len(payload)
	if len(data) > 0 {
		size++ // SSE joins data lines with a newline.
	}
	if size > limit {
		return nil, ErrSSEEventTooLarge
	}
	if len(data) > 0 {
		data = append(data, '\n')
	}
	return append(data, payload...), nil
}

func sseScanError(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return ErrSSEEventTooLarge
	}
	return err
}
