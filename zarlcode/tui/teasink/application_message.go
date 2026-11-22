package teasink

import (
	"encoding/json"
	"slices"

	"github.com/zarldev/zarlmono/zkit/agent/computer"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

func cloneUsage(usage *llm.Usage) *llm.Usage {
	if usage == nil {
		return nil
	}
	return new(*usage)
}

// Receipt payloads outlive transport. Only the rich value forms consumed by the
// renderer, and recursively owned JSON values, may cross that lifetime boundary.
// Unknown results use the text already formatted by the producer, never invoke
// an opaque pointer's methods or retain that borrowed pointer.
func ownedApplicationResult(value any, formatted string) any {
	cloned, ok := cloneApplicationValue(value)
	if !ok {
		return formatted
	}
	return cloned
}

func cloneApplicationValue(value any) (any, bool) {
	switch v := value.(type) {
	case nil, bool, string, json.Number, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return v, true
	case []byte:
		return slices.Clone(v), true
	case []string:
		return slices.Clone(v), true
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			cloned, ok := cloneApplicationValue(item)
			if !ok {
				return nil, false
			}
			result[i] = cloned
		}
		return result, true
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			cloned, ok := cloneApplicationValue(item)
			if !ok {
				return nil, false
			}
			result[key] = cloned
		}
		return result, true
	case code.Plan:
		v.Steps = slices.Clone(v.Steps)
		return v, true
	case code.GrepResult:
		v.Hits = slices.Clone(v.Hits)
		return v, true
	case code.LsResult:
		v.Entries = slices.Clone(v.Entries)
		return v, true
	case code.GlobResult:
		v.Entries = slices.Clone(v.Entries)
		return v, true
	case search.Result:
		v.Hits = slices.Clone(v.Hits)
		v.Suggestions = slices.Clone(v.Suggestions)
		v.Failures = slices.Clone(v.Failures)
		return v, true
	case computer.Observation:
		v.Targets = slices.Clone(v.Targets)
		for i, target := range v.Targets {
			v.Targets[i] = cloneApplicationTarget(target)
		}
		if v.FocusedTarget != nil {
			v.FocusedTarget = new(cloneApplicationTarget(*v.FocusedTarget))
		}
		if v.Screenshot != nil {
			v.Screenshot = new(*v.Screenshot)
		}
		v.Hints = slices.Clone(v.Hints)
		if v.Raw != nil {
			raw, ok := cloneApplicationValue(v.Raw)
			if !ok {
				return nil, false
			}
			v.Raw, ok = raw.(map[string]any)
			if !ok {
				return nil, false
			}
		}
		return v, true
	case programtools.Result:
		output, ok := cloneApplicationValue(v.Output)
		if !ok {
			return nil, false
		}
		v.Output = output
		return v, true
	default:
		return nil, false
	}
}

func cloneApplicationTarget(target computer.TargetDescriptor) computer.TargetDescriptor {
	if target.Bounds != nil {
		target.Bounds = new(*target.Bounds)
	}
	return target
}
