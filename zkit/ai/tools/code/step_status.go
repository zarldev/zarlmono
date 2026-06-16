package code

//go:generate go tool goenums -f step_status.go

// stepStatus is the goenums source for StepStatus — a plan step's
// lifecycle marker, mirroring Codex's update_plan API so GPT-5 family
// models emit the right values. The first token in each trailing comment
// is the canonical wire/log string (what String() and JSON emit); the
// rest are accepted parse aliases, so varied model output ("done",
// "in-progress") still resolves. Callers normalise case and surrounding
// whitespace before parsing (see update_plan).
type stepStatus int

const (
	pending    stepStatus = iota // pending
	inProgress                   // in_progress,in-progress,inprogress
	completed                    // completed,done
)
