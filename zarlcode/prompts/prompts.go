// Package prompts holds the default prompt markdown that ships
// embedded in the binary. The source-of-truth .md files live next to
// this file; go:embed snapshots them at build time so the binary stays
// standalone. Runtime resolution composes these embedded fallbacks with
// per-user preferences or explicit full overrides when present.
package prompts

import _ "embed"

//go:embed common.md
var common string

//go:embed system.md
var systemBody string

// System is the shared operating contract followed by the full BUILD workflow.
var System = common + "\n" + systemBody

//go:embed system_compact.md
var compactBody string

// SystemCompact is the shared operating contract followed by the compact BUILD workflow.
var SystemCompact = common + "\n" + compactBody

//go:embed plan.md
var planBody string

// Plan is the shared operating contract followed by the inspect-only PLAN workflow.
var Plan = common + "\n" + planBody

// Init is the canonical AGENTS.md-authoring prompt the /init command
// sends as a turn (prompts/init.md).
//
//go:embed init.md
var Init string
