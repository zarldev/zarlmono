package sensor

import (
	"context"
	"time"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"
)

// TimeOfDay emits when the wall-clock phase changes between morning,
// afternoon, evening, and night. Zero config, useful as an example sensor and
// as ambient context for the LLM ("morning arrived — greet the speaker").
// Location is used to pick the clock; pass time.Local for the server's tz.
func TimeOfDay(location *time.Location) *zsensor.Func {
	if location == nil {
		location = time.Local
	}
	return zsensor.NewFunc("time_of_day", time.Minute, func(context.Context) (zsensor.Observation, error) {
		now := time.Now().In(location)
		phase := phaseOf(now.Hour())
		return zsensor.Observation{
			Value:  phase,
			Detail: "It is now " + phase + " (" + now.Format("15:04 MST") + ").",
		}, nil
	})
}

func phaseOf(hour int) string {
	switch {
	case hour >= 5 && hour < 12:
		return "morning"
	case hour >= 12 && hour < 17:
		return "afternoon"
	case hour >= 17 && hour < 22:
		return "evening"
	default:
		return "night"
	}
}
