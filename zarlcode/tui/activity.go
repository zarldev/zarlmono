package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Braille spinner frames used for live LLM activity. These are all single-cell
// glyphs in the Unicode Braille Patterns block, so the title width stays stable
// while the indicator animates.
var brailleActivityFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func runActivityGlyph(frame int, running bool) string {
	if !running {
		return "⠄"
	}
	return brailleActivityFrames[frame%len(brailleActivityFrames)]
}

// runActivity is transient presentation state owned by the current top-level
// turn. Tool identity, rather than event order, determines what is still active.
type runActivity struct {
	phase         ActivityPhase
	calls         map[string]activityCall
	waitingInputs bool
}

type activityCall struct {
	name    string
	parent  string
	waiting bool
}

func (s *RunState) acceptsActivity(taskID string, depth int) bool {
	return s.Running && depth == 0 && taskID != "" && taskID == s.activeTopLevel
}

func (s *RunState) observeStream(taskID string, depth int, delta string, phase ActivityPhase) {
	if s.acceptsActivity(taskID, depth) && delta != "" && len(s.activity.calls) == 0 {
		s.activity.phase = phase
	}
}

func (s *RunState) startActivityTool(taskID string, depth int, key, parent, name string) {
	if !s.acceptsActivity(taskID, depth) || key == "" {
		return
	}
	if _, active := s.activity.calls[key]; active {
		return
	}
	if parent != "" {
		if _, active := s.activity.calls[parent]; !active {
			return // Uncorrelated children cannot describe this turn's activity.
		}
	}
	if s.activity.calls == nil {
		s.activity.calls = make(map[string]activityCall)
	}
	s.activity.calls[key] = activityCall{name: name, parent: parent}
	s.activity.phase = ActivityPhases.ACTIVITYWORKING
}

func (s *RunState) finishActivityTool(taskID string, depth int, key string) {
	if s.acceptsActivity(taskID, depth) {
		s.activity.finishTool(key)
	}
}

func (a *runActivity) finishTool(key string) {
	if _, active := a.calls[key]; !active {
		return
	}
	delete(a.calls, key)
	// Finishing a wrapper also retires any children without a terminal event
	// (for example when the wrapper timed out). They must not survive it.
	for child, call := range a.calls {
		if call.parent == key {
			a.finishTool(child)
		}
	}
}

func (s *RunState) waitActivityTool(taskID string, depth int, key string, waiting bool) {
	if !s.acceptsActivity(taskID, depth) {
		return
	}
	if call, active := s.activity.calls[key]; active {
		call.waiting = waiting
		s.activity.calls[key] = call
	}
}

// activityLabel is shared by the transcript and cockpit. Executions take
// precedence over stream observations; silence never implies a new phase.
func (s *RunState) activityLabel() string {
	if !s.Running {
		return "idle"
	}
	if s.activity.waitingInputs {
		return "waiting for agents"
	}
	var key string
	count, waiting := 0, 0
	for id, call := range s.activity.calls {
		if call.parent != "" {
			continue
		}
		key = id
		count++
		if call.waiting {
			waiting++
		}
	}
	if count == 0 {
		return s.activity.phase.String()
	}
	if waiting == count {
		return "waiting for workspace"
	}
	if count > 1 {
		return "running " + itoa(count) + " tools"
	}
	call := s.activity.calls[key]
	// Follow a single executing child; ambiguous or blocked children leave the
	// truthful outer name visible. A blocked child does not prove its wrapper
	// is blocked, and wrappers are never counted alongside their children.
	for {
		childKey, children := "", 0
		for id, child := range s.activity.calls {
			if child.parent == key {
				childKey = id
				children++
			}
		}
		if children != 1 || s.activity.calls[childKey].waiting {
			break
		}
		key = childKey
		call = s.activity.calls[key]
	}
	name := strings.Join(strings.Fields(ansi.Strip(call.name)), " ")
	if name == "" {
		name = "tool"
	}
	return "running " + ansi.Truncate(strings.ToLower(name), 24, "…")
}
