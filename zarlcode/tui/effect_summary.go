package tui

import (
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func effectSummaries(effects []tools.Effect) []string {
	if len(effects) == 0 {
		return nil
	}
	var out []string
	if s := fileEffectSummary(effects); s != "" {
		out = append(out, s)
	}
	if s := processEffectSummary(effects); s != "" {
		out = append(out, s)
	}
	return out
}

func fileEffectSummary(effects []tools.Effect) string {
	var files []tools.FileEffect
	for _, e := range effects {
		if e.Kind == tools.EffectFile && e.File != nil {
			files = append(files, *e.File)
		}
	}
	if len(files) == 0 {
		return ""
	}
	if len(files) == 1 {
		return formatFileEffect(files[0])
	}
	return fmt.Sprintf("changed %d files", len(files))
}

func formatFileEffect(e tools.FileEffect) string {
	switch e.Op {
	case tools.FileCreate:
		return "created " + e.Path
	case tools.FileModify:
		return "modified " + e.Path
	case tools.FileAppend:
		return "appended " + e.Path
	case tools.FileDelete:
		return "deleted " + e.Path
	case tools.FileRename:
		if e.FromPath != "" {
			return "renamed " + e.FromPath + " → " + e.Path
		}
		return "renamed " + e.Path
	case tools.FileRead:
		return "read " + e.Path
	default:
		if e.Path == "" {
			return string(e.Op)
		}
		return strings.TrimSpace(string(e.Op) + " " + e.Path)
	}
}

func processEffectSummary(effects []tools.Effect) string {
	var processes []tools.ProcessEffect
	for _, e := range effects {
		if e.Kind == tools.EffectProcess && e.Process != nil {
			processes = append(processes, *e.Process)
		}
	}
	if len(processes) == 0 {
		return ""
	}
	if len(processes) > 1 {
		return fmt.Sprintf("ran %d processes", len(processes))
	}
	p := processes[0]
	if p.Background {
		if p.ProcessID != "" {
			return "started process " + p.ProcessID
		}
		if p.PID > 0 {
			return fmt.Sprintf("started pid %d", p.PID)
		}
		return "started background process"
	}
	parts := []string{fmt.Sprintf("exit %d", p.ExitCode)}
	if p.TimedOut {
		parts = append(parts, "timed out")
	}
	if p.OutputTruncated {
		parts = append(parts, "output truncated")
	}
	return strings.Join(parts, ", ")
}
