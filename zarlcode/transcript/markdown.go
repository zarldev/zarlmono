package transcript

import (
	"fmt"
	"strings"
	"time"
)

// MarkdownMetadata describes the session header surrounding a thread export.
type MarkdownMetadata struct {
	Title     string
	SessionID string
	Workspace string
	Provider  string
	Model     string
	AgentName string
	CreatedAt time.Time
}

// Markdown renders the canonical thread without consulting any UI projection.
func Markdown(metadata MarkdownMetadata, thread Thread) string {
	var out strings.Builder
	title := strings.Join(strings.Fields(metadata.Title), " ")
	if title == "" {
		title = "Unnamed session"
	}
	fmt.Fprintf(&out, "# %s\n\n", title)
	fmt.Fprintf(&out, "- Session ID: `%s`\n", metadata.SessionID)
	if metadata.Workspace != "" {
		fmt.Fprintf(&out, "- Workspace: `%s`\n", metadata.Workspace)
	}
	if metadata.Provider != "" || metadata.Model != "" {
		model := strings.Trim(strings.TrimSpace(metadata.Provider+" / "+metadata.Model), "/ ")
		fmt.Fprintf(&out, "- Model: `%s`\n", model)
	}
	if metadata.AgentName != "" {
		fmt.Fprintf(&out, "- Agent: `%s`\n", metadata.AgentName)
	}
	if !metadata.CreatedAt.IsZero() {
		fmt.Fprintf(&out, "- Created: %s\n", metadata.CreatedAt.Format(time.RFC3339))
	}
	out.WriteString("\n## Conversation\n")
	for _, entry := range thread.entries {
		heading, body := markdownEntry(entry)
		if body == "" {
			continue
		}
		fmt.Fprintf(&out, "\n### %s\n\n%s", heading, body)
		if !strings.HasSuffix(body, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func markdownEntry(entry Entry) (string, string) {
	payload := entry.Payload
	switch entry.Kind {
	case EntryKinds.ENTRYUSERMESSAGE, EntryKinds.ENTRYQUEUEDUSER:
		return "User", markdownUserPayload(payload)
	case EntryKinds.ENTRYASSISTANTMESSAGE:
		body := payload.Text
		if payload.Interrupted {
			body = strings.TrimSpace(body + "\n\n[interrupted]")
		}
		return "Assistant", body
	case EntryKinds.ENTRYREASONING:
		body := payload.Text
		if payload.Interrupted {
			body = strings.TrimSpace(body + "\n\n[interrupted]")
		}
		return "Reasoning", body
	case EntryKinds.ENTRYTOOLCALL:
		body := payload.ToolName
		if payload.Argument != "" {
			body += " " + payload.Argument
		}
		if payload.ToolState == ToolInterrupted {
			body += "\n\n[interrupted]"
		}
		if payload.Effect != "" {
			body += "\n\n" + payload.Effect
		}
		return "Tool", body
	case EntryKinds.ENTRYDIFF:
		return "Diff", strings.TrimSpace(payload.Path + "\n\n" + payload.Diff)
	case EntryKinds.ENTRYPLAN:
		return "Plan", fmt.Sprintf("%d plan step(s)", len(payload.Plan.Steps))
	case EntryKinds.ENTRYSKILLS:
		return "Skills", strings.Join(payload.Skills, ", ")
	case EntryKinds.ENTRYSUBAGENT:
		body := strings.TrimSpace(payload.AgentName + "\n\n" + payload.Prompt)
		if payload.Subagent == SubagentInterrupted {
			body += "\n\n[interrupted]"
		}
		return "Sub-agent", body
	case EntryKinds.ENTRYNOTICE:
		return "Notice", payload.Text
	default:
		return "", ""
	}
}

func markdownUserPayload(payload Payload) string {
	var body strings.Builder
	body.WriteString(payload.Text)
	for _, attachment := range payload.Attachments {
		if body.Len() > 0 {
			body.WriteByte('\n')
		}
		fmt.Fprintf(&body, "- Attachment: %q", attachment.Name)
		if attachment.MIMEType != "" {
			fmt.Fprintf(&body, " (%s)", attachment.MIMEType)
		}
		if attachment.Size > 0 {
			fmt.Fprintf(&body, " — %d bytes", attachment.Size)
		}
	}
	return body.String()
}
