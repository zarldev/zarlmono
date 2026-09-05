package compact

import _ "embed"

// SummaryDefaultSystemPrompt is the instruction the secondary model
// receives. Targeted at coding-agent history: preserves the user's
// stated goals, ongoing tasks, decisions, file paths, and any
// findings the agent will need to reference downstream. The shell's
// settings may override.
//
//go:embed summary.md
var SummaryDefaultSystemPrompt string

// ExecutiveDefaultSystemPrompt instructs the briefing model. Targets
// a state-handoff voice — third-person, no fluff, every line
// load-bearing. The model's job is to make a fresh-context successor
// agent productive immediately.
//
//go:embed executive.md
var ExecutiveDefaultSystemPrompt string

// HandoverDefaultSystemPrompt instructs the model to write a self-contained
// handover for a fresh agent that will take over with no other context — the
// whole prior conversation is cleared and replaced by this document.
//
//go:embed handover.md
var HandoverDefaultSystemPrompt string
