// Package prompts holds the default prompt markdown that ships
// embedded in the binary. The source-of-truth .md files live next to
// this file; go:embed snapshots them at build time so the binary stays
// standalone. User overrides (workspace, source-tree, per-user) are
// resolved at runtime by the loader in the parent package — these are
// only the fallbacks when no override exists.
package prompts

import _ "embed"

// System is the default agent system prompt (prompts/system.md).
//
//go:embed system.md
var System string

// Plan is the default plan-mode system prompt (prompts/plan.md).
//
//go:embed plan.md
var Plan string

// Init is the canonical AGENTS.md-authoring prompt the /init command
// sends as a turn (prompts/init.md).
//
//go:embed init.md
var Init string
