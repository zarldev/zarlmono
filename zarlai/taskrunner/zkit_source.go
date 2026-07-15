package taskrunner

import (
	"log/slog"

	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// buildTaskSource assembles the per-task tools.Source the zkit runner
// snapshots each iteration: the resolved profile's tools plus the
// lifecycle tools (complete_task / report_progress / pause_task). The
// runner enumerates and executes directly against this source.
//
// A fresh registry per task is intentional: coder profiles bind
// workspace-rooted tools that must not leak across tasks, and
// excludeTools (start_task / schedule_task) are simply never registered
// here rather than refused mid-loop. Dedup is automatic — Registry is
// keyed by tool name, so a lifecycle tool can't be shadowed by a
// same-named profile tool and last-registered wins.
func buildTaskSource(resolved ResolvedProfile, lifecycle []tools.Tool, exclude map[string]bool) *tools.Registry {
	reg := tools.NewRegistry()
	for _, t := range resolved.Tools {
		if exclude[t.Definition().Name.String()] {
			continue
		}
		if err := reg.Register(t); err != nil {
			slog.Warn("task source: skipping profile tool with invalid spec", "name", t.Definition().Name, "error", err)
		}
	}
	// Lifecycle tools register last so they always win a name clash with
	// a profile tool — the loop-control contract must not be overridable
	// by a same-named profile/MCP tool.
	for _, t := range lifecycle {
		if err := reg.Register(t); err != nil {
			slog.Warn("task source: skipping lifecycle tool with invalid spec", "name", t.Definition().Name, "error", err)
		}
	}
	return reg
}
