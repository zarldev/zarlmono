package sensor

import (
	"context"
	"fmt"
	"time"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"

	"github.com/zarldev/zarlmono/zarlai/service"
)

// FromTool wraps an arbitrary registered tool as a periodic Sensor. Each tick
// invokes tool.Execute and treats the result's text as the observed value.
// Errors are bubbled up to the Runner, which logs and retries. If the tool
// reports a failure result, that's also treated as a poll failure.
//
// key is the sensor identifier used in notifications ("sensor:<key>") — it
// doubles as the change-detection key, so pick something stable and unique.
func FromTool(key string, tool tools.Tool, args service.Arguments, interval time.Duration) *zsensor.Func {
	return zsensor.NewFunc(key, interval, func(ctx context.Context) (zsensor.Observation, error) {
		name := tool.Definition().Name
		call := tools.ToolCall{
			ToolName:  name,
			Arguments: tools.ToolParameters(args),
		}
		result, err := tool.Execute(ctx, call)
		if err != nil {
			return zsensor.Observation{}, fmt.Errorf("%s: %w", name.String(), err)
		}
		if result != nil && !result.Success {
			return zsensor.Observation{}, fmt.Errorf("%s: %s", name.String(), result.Error)
		}
		return zsensor.Observation{Value: service.ToolResultText(result)}, nil
	})
}
