package prompts

import "strings"

// FragmentKind identifies one source of text that contributes to a rendered
// prompt stack. Kinds are intentionally descriptive strings so UI consumers can
// display them without a translation table.
type FragmentKind string

const (
	// FragmentSystem is the active build-mode system prompt template/body.
	FragmentSystem FragmentKind = "system"
	// FragmentPlan is the active plan-mode system prompt template/body.
	FragmentPlan FragmentKind = "plan"
	// FragmentWorkspaceInstruction is repository or workspace guidance appended
	// to rendered prompts.
	FragmentWorkspaceInstruction FragmentKind = "workspace_instruction"
	// FragmentSkill is a discovered skill body.
	FragmentSkill FragmentKind = "skill"
	// FragmentAgent is a named sub-agent prompt body.
	FragmentAgent FragmentKind = "agent"
	// FragmentUserPreferences is additive per-user guidance appended to rendered
	// prompts from ~/.zarlcode/preferences.md.
	FragmentUserPreferences FragmentKind = "user_preferences"
	// FragmentRenderedTotal is the fully-rendered prompt sent as the system
	// message for the next run.
	FragmentRenderedTotal FragmentKind = "rendered_total"
)

// Fragment is neutral accounting metadata for prompt text. It records where the
// text came from and its rough size; it deliberately does not judge writing
// quality or enforce policy.
type Fragment struct {
	Kind        FragmentKind
	Name        string
	Source      string
	Reason      string
	Order       int
	Bytes       int
	Words       int
	Lines       int
	Contributes bool
}

// Stack is the ordered set of prompt fragments visible to inspection tooling.
// Total* fields summarize fragments marked as contributing to the assembled
// prompt surface; the rendered-total fragment is excluded from those totals so
// it can be displayed without double-counting.
type Stack struct {
	Fragments  []Fragment
	TotalBytes int
	TotalWords int
	TotalLines int

	RenderedBytes int
	RenderedWords int
	RenderedLines int
}

// NewFragment measures body and returns prompt fragment accounting metadata.
func NewFragment(kind FragmentKind, name, source, reason string, order int, body string, contributes bool) Fragment {
	return Fragment{
		Kind:        kind,
		Name:        name,
		Source:      source,
		Reason:      reason,
		Order:       order,
		Bytes:       len([]byte(body)),
		Words:       len(strings.Fields(body)),
		Lines:       lineCount(body),
		Contributes: contributes,
	}
}

// NewStack returns a stack with total sizes computed from contributing
// fragments, excluding rendered-total entries to avoid double-counting.
func NewStack(fragments []Fragment) Stack {
	out := Stack{Fragments: append([]Fragment(nil), fragments...)}
	for _, f := range out.Fragments {
		if f.Kind == FragmentRenderedTotal {
			out.RenderedBytes = f.Bytes
			out.RenderedWords = f.Words
			out.RenderedLines = f.Lines
			continue
		}
		if !f.Contributes {
			continue
		}
		out.TotalBytes += f.Bytes
		out.TotalWords += f.Words
		out.TotalLines += f.Lines
	}
	return out
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
