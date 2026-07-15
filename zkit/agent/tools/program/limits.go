package program

import (
	"errors"
	"time"
)

func defaultLimits() Limits {
	return Limits{
		MaxScriptBytes:     32 << 10,
		MaxExecutionSteps:  100_000,
		MaxToolCalls:       20,
		MaxParallelCalls:   8,
		MaxToolResultBytes: 256 << 10,
		MaxOutputBytes:     64 << 10,
		Timeout:            time.Minute,
	}
}

func normalizeLimits(in Limits) (Limits, error) {
	defaults := defaultLimits()
	out := in
	if out.MaxScriptBytes < 0 || out.MaxToolCalls < 0 || out.MaxParallelCalls < 0 || out.MaxToolResultBytes < 0 || out.MaxOutputBytes < 0 || out.Timeout < 0 {
		return Limits{}, errors.New("program limits: negative values are invalid")
	}
	if out.MaxScriptBytes == 0 {
		out.MaxScriptBytes = defaults.MaxScriptBytes
	}
	if out.MaxExecutionSteps == 0 {
		out.MaxExecutionSteps = defaults.MaxExecutionSteps
	}
	if out.MaxToolCalls == 0 {
		out.MaxToolCalls = defaults.MaxToolCalls
	}
	if out.MaxParallelCalls == 0 {
		out.MaxParallelCalls = defaults.MaxParallelCalls
	}
	if out.MaxToolResultBytes == 0 {
		out.MaxToolResultBytes = defaults.MaxToolResultBytes
	}
	if out.MaxOutputBytes == 0 {
		out.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if out.Timeout == 0 {
		out.Timeout = defaults.Timeout
	}
	if out.MaxParallelCalls > out.MaxToolCalls {
		return Limits{}, errors.New("program limits: MaxParallelCalls cannot exceed MaxToolCalls")
	}
	return out, nil
}
