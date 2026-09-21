package transcript

// InspectRecords decodes structurally valid canonical history without repairing
// lifecycle state or advancing revisions. Unlike CheckpointFromRecords it permits
// unfinished entries for read-only viewing; success is not permission to resume.
// Malformed or unsupported records return ErrInvalidCheckpoint without private
// payloads or parser diagnostics. Returned entries own their decoded data.
func InspectRecords(revision uint64, records []Record) (Thread, error) {
	thread, err := fromRecords(revision, records, false)
	if err != nil {
		return Thread{}, ErrInvalidCheckpoint
	}
	for _, entry := range thread.entries {
		if !checkpointPayloadValid(entry) {
			return Thread{}, ErrInvalidCheckpoint
		}
	}
	return thread, nil
}

// UnfinishedEntryCount counts active streams, tools, subagents, and undelivered
// queued input without changing their state. Zero alone does not prove that the
// thread and saved provider context form an exact continuation.
func (t Thread) UnfinishedEntryCount() int {
	count := 0
	for _, entry := range t.entries {
		switch entry.Kind {
		case EntryKinds.ENTRYASSISTANTMESSAGE, EntryKinds.ENTRYREASONING:
			if !entry.Payload.Complete && !entry.Payload.Interrupted {
				count++
			}
		case EntryKinds.ENTRYTOOLCALL:
			if entry.Payload.ToolState == ToolRunning {
				count++
			}
		case EntryKinds.ENTRYSUBAGENT:
			if entry.Payload.Subagent == SubagentPending || entry.Payload.Subagent == SubagentRunning {
				count++
			}
		case EntryKinds.ENTRYQUEUEDUSER:
			if !entry.Payload.Injected {
				count++
			}
		}
	}
	return count
}
