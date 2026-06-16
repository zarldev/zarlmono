package subscribers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tools "github.com/zarldev/zarlmono/zkit/ai/tools"

	"github.com/zarldev/zarlmono/zarlai/events"
)

const obsidianAppendTool = "obsidian_append_content"

// ObsidianTools is the small slice of the tool registry this handler
// needs — defined here consumer-side so tests don't need the full
// tools.Registry. Implementations return false when the named tool
// isn't available, and the handler silently skips.
type ObsidianTools interface {
	Tool(name tools.ToolName) (tools.Tool, bool)
}

// PersonPageKeeper appends a chronological entry to People/<Name>.md
// whenever a session ends with a known identity. Pairs with the
// research-task append already done by the taskrunner so a person's
// page accumulates both conversations and research over time.
type PersonPageKeeper struct {
	tools ObsidianTools
}

func NewPersonPageKeeper(tools ObsidianTools) *PersonPageKeeper {
	return &PersonPageKeeper{tools: tools}
}

// Handle processes a SessionEnded event. No-op when the session was
// anonymous (no person name) or the Obsidian append tool isn't
// registered in this build.
func (k *PersonPageKeeper) Handle(ctx context.Context, e events.Event) error {
	p, ok := e.Payload.(events.SessionEndedPayload)
	if !ok {
		return fmt.Errorf("person-page keeper: unexpected payload type %T", e.Payload)
	}
	if p.PersonName == "" {
		return nil
	}
	if k.tools == nil {
		return nil
	}
	tool, ok := k.tools.Tool(obsidianAppendTool)
	if !ok {
		// Obsidian MCP provider isn't configured — silently skip so
		// installs without Obsidian don't log noise per session end.
		return nil
	}

	dateSlug := time.Now().Format("2006-01-02")
	line := fmt.Sprintf("- %s — conversation with %d turns", dateSlug, len(p.Messages))
	if p.SessionID != "" {
		line += fmt.Sprintf(" (session `%s`)", p.SessionID)
	}

	result, err := tool.Execute(ctx, tools.ToolCall{
		ToolName: obsidianAppendTool,
		Arguments: tools.ToolParameters{
			"filepath": fmt.Sprintf("People/%s.md", p.PersonName),
			"content":  line + "\n",
		},
	})
	switch {
	case err != nil:
		slog.WarnContext(ctx, "person page keeper: append failed", "person", p.PersonName, "err", err)
	case result != nil && !result.Success:
		slog.WarnContext(ctx, "person page keeper: append failed", "person", p.PersonName, "err", result.Error)
	}
	return nil
}
