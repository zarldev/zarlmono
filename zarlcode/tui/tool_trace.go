package tui

import (
	"encoding/json"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const toolTraceVersion = 1
const maxSavedToolResultBytes = 16 << 10
const maxSavedToolTraceBytes = 256 << 10

type traceRestoreStatus int

const (
	traceRestoreEmpty traceRestoreStatus = iota
	traceRestoreApplied
	traceRestoreUnsupported
	traceRestoreMalformed
)

type savedToolTrace struct {
	Version        int             `json:"version"`
	Items          []savedToolItem `json:"items"`
	TraceTruncated bool            `json:"trace_truncated,omitempty"`
}

type savedToolItem struct {
	TaskID        string        `json:"task_id,omitempty"`
	ToolID        string        `json:"tool_id"`
	ParentToolID  string        `json:"parent_tool_id,omitempty"`
	Sequence      int           `json:"sequence,omitempty"`
	Depth         int           `json:"depth,omitempty"`
	Name          string        `json:"name"`
	Arg           string        `json:"arg,omitempty"`
	State         toolState     `json:"state"`
	FailKind      tools.Kind    `json:"fail_kind,omitempty"`
	Result        string        `json:"result,omitempty"`
	Data          any           `json:"data,omitempty"`
	Duration      time.Duration `json:"duration,omitempty"`
	Expanded      bool          `json:"expanded,omitempty"`
	Effect        string        `json:"effect,omitempty"`
	Truncated     bool          `json:"truncated,omitempty"`
	OriginalBytes int           `json:"original_bytes,omitempty"`
}

func buildSavedToolTrace(tl *timeline) savedToolTrace {
	trace := savedToolTrace{Version: toolTraceVersion}
	for _, it := range tl.items {
		g, ok := it.(*groupItem)
		if !ok || g.kind != groupTools {
			continue
		}
		for _, child := range g.children {
			tool, ok := child.(*toolItem)
			if !ok {
				continue
			}
			appendSavedToolItem(&trace.Items, tool, tl.toolIdx)
		}
	}
	return trace
}

func appendSavedToolItem(dst *[]savedToolItem, tool *toolItem, idx map[string]toolRef) {
	if tool == nil {
		return
	}
	entry := savedToolItem{
		Depth:    tool.depth,
		Name:     tool.name,
		Arg:      tool.arg,
		State:    tool.state,
		FailKind: tool.failKind,
		Result:   tool.result,
		Data:     clampSavedToolData(tool.data),
		Duration: tool.dur,
		Expanded: tool.expanded,
		Effect:   tool.effect,
	}
	for id, ref := range idx {
		if ref.tool == tool {
			entry.ToolID = id
			break
		}
	}
	entry.Sequence = tool.sequence
	*dst = append(*dst, entry)
	for _, child := range tool.children {
		childEntry := savedToolItem{
			Depth:        child.depth,
			Name:         child.name,
			Arg:          child.arg,
			State:        child.state,
			FailKind:     child.failKind,
			Result:       child.result,
			Data:         clampSavedToolData(child.data),
			Duration:     child.dur,
			Expanded:     child.expanded,
			Effect:       child.effect,
			ParentToolID: entry.ToolID,
			Sequence:     child.sequence,
		}
		for id, ref := range idx {
			if ref.tool == child {
				childEntry.ToolID = id
				break
			}
		}
		*dst = append(*dst, childEntry)
	}
}

func clampSavedToolData(data any) any {
	if data == nil {
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return map[string]any{"truncated": true, "reason": err.Error()}
	}
	if len(b) <= maxSavedToolResultBytes {
		return data
	}
	return map[string]any{"truncated": true, "original_bytes": len(b), "preview": string(b[:maxSavedToolResultBytes])}
}

func encodeToolTraceJSON(tl *timeline) []byte {
	trace := buildSavedToolTrace(tl)
	b, err := json.Marshal(trace)
	if err != nil || len(b) == 0 {
		return []byte("null")
	}
	if len(b) > maxSavedToolTraceBytes {
		trimmed := savedToolTrace{Version: trace.Version}
		trimmed.TraceTruncated = true
		for _, item := range trace.Items {
			trimmed.Items = append(trimmed.Items, item)
			blob, merr := json.Marshal(trimmed)
			if merr != nil || len(blob) > maxSavedToolTraceBytes {
				break
			}
		}
		b, _ = json.Marshal(trimmed)
	}
	return b
}

func restoreToolTrace(tl *timeline, raw []byte) traceRestoreStatus {
	if len(raw) == 0 || string(raw) == "null" {
		return traceRestoreEmpty
	}
	var trace savedToolTrace
	if err := json.Unmarshal(raw, &trace); err != nil {
		return traceRestoreMalformed
	}
	if trace.Version == 0 || len(trace.Items) == 0 {
		return traceRestoreEmpty
	}
	if trace.Version != toolTraceVersion {
		return traceRestoreUnsupported
	}
	parentsByOrder := topLevelToolRefs(tl)
	for _, entry := range trace.Items {
		if entry.ParentToolID != "" {
			continue
		}
		ref, ok := tl.toolIdx[entry.ToolID]
		if !ok || ref.tool == nil {
			if len(parentsByOrder) == 0 {
				continue
			}
			ref = parentsByOrder[0]
			parentsByOrder = parentsByOrder[1:]
		}
		applySavedToolFields(ref.tool, entry)
		if entry.ToolID != "" {
			tl.toolIdx[entry.ToolID] = ref
		}
	}
	for _, entry := range trace.Items {
		if entry.ParentToolID == "" {
			continue
		}
		parentRef, ok := tl.toolIdx[entry.ParentToolID]
		if !ok || parentRef.tool == nil {
			if len(parentsByOrder) > 0 {
				parentRef = parentsByOrder[0]
				parentsByOrder = parentsByOrder[1:]
				ok = true
			}
		}
		if !ok || parentRef.tool == nil {
			continue
		}
		if hasChild(parentRef.tool, entry.ToolID, entry.Sequence, entry.Name) {
			continue
		}
		child := &toolItem{}
		applySavedToolFields(child, entry)
		if child.depth == 0 {
			child.depth = parentRef.tool.depth + 1
		}
		tl.attachChildTool(parentRef, entry.ToolID, child, entry.Sequence)
	}
	return traceRestoreApplied
}

func applySavedToolFields(tool *toolItem, entry savedToolItem) {
	tool.depth = entry.Depth
	tool.name = entry.Name
	tool.arg = entry.Arg
	tool.state = entry.State
	tool.failKind = entry.FailKind
	tool.result = entry.Result
	tool.data = entry.Data
	tool.dur = entry.Duration
	tool.expanded = entry.Expanded
	tool.effect = entry.Effect
	tool.sequence = entry.Sequence
}

func topLevelToolRefs(tl *timeline) []toolRef {
	var refs []toolRef
	for _, it := range tl.items {
		g, ok := it.(*groupItem)
		if !ok || g.kind != groupTools {
			continue
		}
		for _, child := range g.children {
			if tool, ok := child.(*toolItem); ok {
				refs = append(refs, toolRef{group: g, tool: tool})
			}
		}
	}
	return refs
}

func hasChild(parent *toolItem, toolID string, sequence int, name string) bool {
	for _, child := range parent.children {
		if child == nil {
			continue
		}
		if child.sequence == sequence && child.name == name {
			return true
		}
		_ = toolID
	}
	return false
}
