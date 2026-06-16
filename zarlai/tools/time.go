package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	tools "github.com/zarldev/zarlmono/zkit/ai/tools"

	"github.com/zarldev/zarlmono/zarlai/service"
)

// TimeTool returns the current time, optionally in a specific timezone.
type TimeTool struct{}

func NewTimeTool() *TimeTool { return &TimeTool{} }

func (t *TimeTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "current_time",
		Description: "Return the real-world current date and time. Call this whenever the user asks \"what time is it\", \"what's today's date\", \"what day is it\", or needs time-of-day reasoning (is it morning? is it late? how long until X?). Also call it before answering any question where \"today\", \"now\", \"this week\", \"tonight\" matters — don't guess from your training cutoff. Supports any IANA timezone (\"America/New_York\", \"Europe/London\", \"Asia/Tokyo\") and common abbreviations (\"PST\", \"JST\", \"GMT\").",
		Parameters: service.Parameters{
			{Name: "timezone", Type: service.ParamString, Description: "IANA timezone name (preferred) or common abbreviation. Omit for the server's local time.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *TimeTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	tz := call.Arguments.String("timezone", "")

	var now time.Time
	var zoneName string

	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			// Try common abbreviations
			loc = matchCommonTimezone(tz)
			if loc == nil {
				return tools.Failure(call.ID, tools.Validation("current_time", fmt.Sprintf("unknown timezone %q: %v", tz, err))), nil
			}
		}
		now = time.Now().In(loc)
		zoneName = tz
	} else {
		now = time.Now()
		zoneName = now.Location().String()
	}

	return tools.Success(call.ID, fmt.Sprintf("%s (%s)", now.Format("Monday, January 2, 2006 3:04 PM MST"), zoneName)), nil
}

func matchCommonTimezone(abbr string) *time.Location {
	common := map[string]string{
		"est": "America/New_York", "cst": "America/Chicago", "mst": "America/Denver",
		"pst": "America/Los_Angeles", "gmt": "Europe/London", "bst": "Europe/London",
		"cet": "Europe/Paris", "ist": "Asia/Kolkata", "jst": "Asia/Tokyo",
		"aest": "Australia/Sydney", "nzst": "Pacific/Auckland", "utc": "UTC",
	}
	if iana, ok := common[strings.ToLower(abbr)]; ok {
		loc, _ := time.LoadLocation(iana)
		return loc
	}
	return nil
}
