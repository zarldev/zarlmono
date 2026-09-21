package tui

import (
	"time"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func (tl *timeline) applyTranscript(event any) transcript.Change {
	change, err := tl.thread.Apply(event)
	if err != nil {
		panic("apply controlled transcript event: " + err.Error())
	}
	if change.Changed() {
		tl.pendingPersist = transcript.StrongerPersistence(tl.pendingPersist, change.Persistence)
	}
	return change
}

func transcriptAttachments(metadata []attachmentMetadata) []transcript.Attachment {
	attachments := make([]transcript.Attachment, len(metadata))
	for i, attachment := range metadata {
		attachments[i] = transcript.Attachment{
			Name: attachment.Name, MIMEType: attachment.MIMEType, Size: attachment.Size,
		}
	}
	return attachments
}

func (tl *timeline) takeTranscriptPersistence() transcript.Persistence {
	policy := tl.pendingPersist
	tl.pendingPersist = transcript.Persistences.PERSISTENCENONE
	return policy
}

func (tl *timeline) clearTranscriptPersistence() {
	tl.pendingPersist = transcript.Persistences.PERSISTENCENONE
}

func (tl *timeline) transcriptThread() transcript.Thread {
	return tl.thread.Thread()
}

func (tl *timeline) addUserWithAttachments(text string, attachments []attachmentMetadata) {
	tl.addUserTranscript(text, transcriptAttachments(attachments))
}

func (tl *timeline) addUserTranscript(text string, attachments []transcript.Attachment) {
	change := tl.applyTranscript(transcript.UserSubmitted{Text: text, Attachments: attachments})
	if text != "" || len(attachments) > 0 {
		tl.appendItem(&userItem{entryID: change.PrimaryEntryID, text: text, attachments: append([]transcript.Attachment(nil), attachments...)})
	}
}

func (tl *timeline) restoreThread(thread transcript.Thread) {
	tl.Clear()
	tl.thread = transcript.NewReducerFrom(thread)

	items := make(map[string]item, len(thread.Entries()))
	subagents := make(map[string]*subAgentItem)
	subToolGroups := make(map[string]*groupItem)
	subEditGroups := make(map[string]*groupItem)
	var topTools, topEdits, topAgents *groupItem
	turnAssistants := make(map[string]*assistantItem)
	turnThinking := make(map[[2]string]*thinkingItem)
	var latestRootTurn string
	ensureThinking := func(entry transcript.Entry) *thinkingItem {
		turnID := entry.TurnID
		if turnID == "" && entry.ParentID == "" {
			// Historical mode notices did not carry a turn ID.
			turnID = latestRootTurn
		}
		key := [2]string{turnID, entry.ParentID}
		if turnID == "" {
			key[0] = entry.ID
		}
		it := turnThinking[key]
		if it == nil {
			it = &thinkingItem{done: true, nested: true}
			turnThinking[key] = it
			appendProjectedItem(tl, entry.ParentID, it, subagents, &topTools, &topEdits)
		}
		markRestoredTurnActivity(turnAssistants, turnID)
		return it
	}

	appendTop := func(it item) {
		tl.appendItem(it)
		topTools, topEdits = nil, nil
	}
	for _, entry := range thread.Entries() {
		payload := entry.Payload
		switch entry.Kind {
		case transcript.EntryKinds.ENTRYUSERMESSAGE:
			latestRootTurn = ""
			it := &userItem{entryID: entry.ID, text: payload.Text, attachments: append([]transcript.Attachment(nil), payload.Attachments...)}
			appendTop(it)
			items[entry.ID] = it
		case transcript.EntryKinds.ENTRYQUEUEDUSER:
			it := &queuedUserItem{text: payload.Text, injected: payload.Injected}
			appendTop(it)
			items[entry.ID] = it
		case transcript.EntryKinds.ENTRYASSISTANTMESSAGE:
			it := &assistantItem{content: payload.Text, done: true, interrupted: payload.Interrupted}
			appendProjectedItem(tl, entry.ParentID, it, subagents, &topTools, &topEdits)
			items[entry.ID] = it
			if entry.TurnID != "" {
				turnAssistants[entry.TurnID] = it
				if entry.ParentID == "" {
					latestRootTurn = entry.TurnID
				}
			}
		case transcript.EntryKinds.ENTRYREASONING:
			it := ensureThinking(entry)
			it.text += payload.Text
			it.interrupted = it.interrupted || payload.Interrupted
			items[entry.ID] = it
		case transcript.EntryKinds.ENTRYSKILLS:
			sk := make([]skillRef, len(payload.Skills))
			for i, name := range payload.Skills {
				sk[i] = skillRef{name: name}
			}
			it := &skillsItem{skills: sk, closed: true, nested: entry.ParentID != ""}
			appendProjectedItem(tl, entry.ParentID, it, subagents, &topTools, &topEdits)
			items[entry.ID] = it
			markRestoredTurnActivity(turnAssistants, entry.TurnID)
		case transcript.EntryKinds.ENTRYTOOLCALL:
			state := toolRunning
			switch payload.ToolState {
			case transcript.ToolSucceeded:
				state = toolOK
			case transcript.ToolFailed:
				state = toolFailed
			case transcript.ToolInterrupted:
				state = toolInterrupted
			}
			failureKind, err := tools.ParseKind(payload.FailureKind)
			if err != nil {
				failureKind = tools.Kinds.UNKNOWN
			}
			it := &toolItem{
				name: payload.ToolName, arg: payload.Argument, effect: payload.Effect,
				state: state, failKind: failureKind,
				dur:      time.Duration(payload.DurationMS) * time.Millisecond,
				sequence: payload.Sequence,
			}
			if parentTool, ok := items[entry.ParentID].(*toolItem); ok {
				it.notify = parentTool.bump
				insertChildBySequence(parentTool, it, payload.Sequence)
			} else if parent := subagents[entry.ParentID]; parent != nil {
				group := subToolGroups[entry.ParentID]
				if group == nil {
					group = &groupItem{kind: groupTools, nested: true, closed: true, notify: parent.bump}
					parent.children = append(parent.children, group)
					subToolGroups[entry.ParentID] = group
				}
				it.notify = group.bump
				group.children = append(group.children, it)
			} else {
				if topTools == nil {
					topTools = &groupItem{kind: groupTools, nested: true, closed: true}
					topTools.notify = func() { tl.invalidateItem(topTools) }
					tl.appendItem(topTools)
				}
				it.notify = topTools.bump
				topTools.children = append(topTools.children, it)
			}
			items[entry.ID] = it
			markRestoredTurnActivity(turnAssistants, entry.TurnID)
		case transcript.EntryKinds.ENTRYDIFF:
			it := &diffItem{path: payload.Path, diff: payload.Diff}
			if parent := subagents[entry.ParentID]; parent != nil {
				group := subEditGroups[entry.ParentID]
				if group == nil {
					group = &groupItem{kind: groupEdits, nested: true, closed: true, notify: parent.bump}
					parent.children = append(parent.children, group)
					subEditGroups[entry.ParentID] = group
				}
				group.children = append(group.children, it)
			} else {
				if topEdits == nil {
					topEdits = &groupItem{kind: groupEdits, nested: true, closed: true}
					topEdits.notify = func() { tl.invalidateItem(topEdits) }
					tl.appendItem(topEdits)
				}
				topEdits.children = append(topEdits.children, it)
			}
			items[entry.ID] = it
			markRestoredTurnActivity(turnAssistants, entry.TurnID)
		case transcript.EntryKinds.ENTRYPLAN:
			it := &planItem{plan: payload.Plan, nested: entry.ParentID != ""}
			appendProjectedItem(tl, entry.ParentID, it, subagents, &topTools, &topEdits)
			items[entry.ID] = it
			markRestoredTurnActivity(turnAssistants, entry.TurnID)
		case transcript.EntryKinds.ENTRYSUBAGENT:
			it := &subAgentItem{
				agentName: payload.AgentName, provider: payload.Provider, model: payload.Model,
				prompt: firstLine(payload.Prompt), taskID: entry.TurnID,
				closed: true, pending: false, launchFailed: payload.Subagent == transcript.SubagentFailed, interrupted: payload.Subagent == transcript.SubagentInterrupted,
				toolIdx: make(map[string]toolRef),
			}
			if topAgents == nil {
				topAgents = &groupItem{kind: groupAgents, nested: true, closed: true}
				topAgents.notify = func() { tl.invalidateItem(topAgents) }
				tl.appendItem(topAgents)
			}
			it.notify = topAgents.bump
			topAgents.children = append(topAgents.children, it)
			items[entry.ID] = it
			subagents[entry.ID] = it
		case transcript.EntryKinds.ENTRYINPUTADMISSION, transcript.EntryKinds.ENTRYINPUTWAIT:
			// Historical facts render without rebuilding live wait/task state.
			if entry.Kind == transcript.EntryKinds.ENTRYINPUTADMISSION && payload.InputAdmission.Namespace == "spawn.completion" {
				tl.appendAgentAdmission(payload.InputAdmission.ID, payload.Text)
				continue
			}
			it := &noticeItem{text: payload.Text}
			appendProjectedItem(tl, entry.ParentID, it, subagents, &topTools, &topEdits)
			items[entry.ID] = it
		case transcript.EntryKinds.ENTRYNOTICE:
			if mode, ok := restoredModeNotice(payload.Text); ok {
				think := ensureThinking(entry)
				think.children = append(think.children, mode)
				items[entry.ID] = mode
				continue
			}
			it := &noticeItem{text: payload.Text}
			appendProjectedItem(tl, entry.ParentID, it, subagents, &topTools, &topEdits)
			items[entry.ID] = it
		}
	}
	for _, it := range tl.items {
		wireTranscriptItem(it, nil)
	}
	tl.clearTranscriptPersistence()
}

func appendProjectedItem(
	tl *timeline,
	parentID string,
	it item,
	subagents map[string]*subAgentItem,
	topTools **groupItem,
	topEdits **groupItem,
) {
	if parent := subagents[parentID]; parent != nil {
		parent.children = append(parent.children, it)
		wireTranscriptItem(it, parent.bump)
		return
	}
	tl.appendItem(it)
	*topTools, *topEdits = nil, nil
}

func markRestoredTurnActivity(turns map[string]*assistantItem, turnID string) {
	if turn := turns[turnID]; turn != nil {
		turn.hasActivity = true
	}
}

func wireTranscriptItem(it item, parent versionNotifier) {
	switch typed := it.(type) {
	case *groupItem:
		typed.notify = parent
		for _, child := range typed.children {
			wireTranscriptItem(child, typed.bump)
		}
	case *toolItem:
		typed.notify = parent
		for _, child := range typed.children {
			wireTranscriptItem(child, typed.bump)
		}
	case *subAgentItem:
		typed.notify = parent
		for _, child := range typed.children {
			wireTranscriptItem(child, typed.bump)
		}
	}
}
