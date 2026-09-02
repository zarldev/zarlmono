package transcript

//go:generate go tool goenums -f kind_enum.go

// entryKind is the goenums source for EntryKind. It identifies durable thread
// semantics independently of any renderer or persistence layout.
type entryKind int

const (
	entryUserMessage      entryKind = iota // user_message
	entryQueuedUser                        // queued_user
	entryAssistantMessage                  // assistant_message
	entryReasoning                         // reasoning
	entryToolCall                          // tool_call
	entryDiff                              // diff
	entryPlan                              // plan
	entrySkills                            // skills
	entrySubagent                          // subagent
	entryNotice                            // notice
)
