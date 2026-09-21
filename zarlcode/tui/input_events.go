package tui

import (
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
)

func (m *UI) applyInputEvent(event any) {
	switch event := event.(type) {
	case teasink.WaitingForInputsMsg:
		m.applyWaitingForInputs(event)
	case teasink.InputsAdmittedMsg:
		m.applyInputsAdmitted(event)
	}
}

func (m *UI) applyWaitingForInputs(event teasink.WaitingForInputsMsg) {
	if m.session.Run.acceptsActivity(event.TaskID, event.Depth) {
		m.session.Run.activity.waitingInputs = event.Waiting
	} else if event.Depth == 0 || !m.session.Run.Running {
		return
	} else if subagent := m.timeline.subAgent(event.TaskID); subagent == nil || subagent.closed {
		return
	}
	text := "Child-result wait ended."
	if event.Waiting {
		text = "Child-result wait started."
	}
	m.timeline.applyTranscript(transcript.InputWaitChanged{TurnID: event.TaskID, Text: text, Waiting: event.Waiting})
	m.timeline.appendNoticeForTurn(event.TaskID, text)
}

func (m *UI) applyInputsAdmitted(event teasink.InputsAdmittedMsg) {
	if !m.session.Run.Running || (event.Depth == 0 && !m.session.Run.acceptsActivity(event.TaskID, event.Depth)) {
		return
	}
	if event.Depth > 0 {
		subagent := m.timeline.subAgent(event.TaskID)
		if subagent == nil || subagent.closed {
			return
		}
	}
	// Typed admission facts survive replay without recreating live group state.
	for _, message := range event.Messages {
		var completion spawn.CompletionObservation
		if message.Observation.Version != 1 || json.Unmarshal([]byte(message.Content), &completion) != nil || completion.Kind != "agent_completion" {
			continue
		}
		text := fmt.Sprintf("Child %s: %s result admitted — reported evidence from its original assignment.", completion.Child, completion.State)
		for _, reference := range event.References {
			if reference.ID == string(completion.Child) {
				m.timeline.applyTranscript(transcript.InputAdmitted{TurnID: event.TaskID, Text: text, Admission: transcript.InputAdmission{Namespace: reference.Namespace, ID: reference.ID}})
				m.timeline.appendAgentAdmission(reference.ID, text)
			}
		}
	}
	if len(event.Messages) == 0 {
		for _, reference := range event.References {
			text := fmt.Sprintf("Result %s/%s admitted through explicit tool output.", reference.Namespace, reference.ID)
			m.timeline.applyTranscript(transcript.InputAdmitted{TurnID: event.TaskID, Text: text, Admission: transcript.InputAdmission{Namespace: reference.Namespace, ID: reference.ID, Explicit: true}})
			if reference.Namespace == "spawn.completion" {
				m.timeline.appendAgentAdmission(reference.ID, text)
			} else {
				m.timeline.appendNoticeForTurn(event.TaskID, text)
			}
		}
	}
}

// Admission follows completion, after the child leaves the active routing map.
// Find its retained row without reopening it or adding another lifecycle index.
func (tl *timeline) appendAgentAdmission(taskID, text string) {
	for i := len(tl.items) - 1; i >= 0; i-- {
		agents, ok := tl.items[i].(*groupItem)
		if !ok || agents.kind != groupAgents {
			continue
		}
		for j := len(agents.children) - 1; j >= 0; j-- {
			agent, ok := agents.children[j].(*subAgentItem)
			if ok && agent.taskID == taskID {
				agent.addNotice(text)
				return
			}
		}
	}
	// An orphan admission remains in canonical history, but must not create a
	// fake agent or a standalone comment in the main conversation.
}
