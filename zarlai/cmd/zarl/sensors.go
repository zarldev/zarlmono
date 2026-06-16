package main

import (
	"context"
	"log/slog"
	"time"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/sensor"
	transportgrpc "github.com/zarldev/zarlmono/zarlai/transport/grpc"
)

// buildSensors assembles the live sensor subsystem: registers the built-in
// TimeOfDay sensor, activates every approved proposal through the
// controller (which knows how to materialize each Kind), and wires
// change events into the notification store as broadcast notifications.
// Returns the runner so main() can Start/Stop it.
func buildSensors(
	ctx context.Context,
	controller *SensorController,
	notifications *znotify.NotificationStore,
	sensorProposals *repository.SensorProposalRepo,
	toolMgr *transportgrpc.ToolManager,
) *zsensor.Runner {
	runner := controller.Runner()
	if err := runner.Register(sensor.TimeOfDay(time.Local)); err != nil {
		slog.WarnContext(ctx, "register time_of_day sensor", "error", err)
	}

	// Spotify now-playing is only available when the spotify provider is
	// configured and its OAuth cache is populated. When absent (no creds,
	// or first run before `cmd/spotify-auth`), the sensor simply isn't
	// registered — the UI strip stays hidden.
	if sc := toolMgr.SpotifyClient(); sc != nil {
		if err := runner.Register(sensor.SpotifyNowPlaying(sc)); err != nil {
			slog.WarnContext(ctx, "register spotify sensor", "error", err)
		}
	}

	// Approved proposals get materialized through the controller so each
	// kind ends up on the right path (poll vs reactive). Failures are
	// logged and skipped — one bad proposal must not stop the rest.
	if approved, err := sensorProposals.ListApproved(ctx); err != nil {
		slog.WarnContext(ctx, "load approved sensor proposals", "error", err)
	} else {
		controller.ActivateAllApproved(approved)
	}

	runner.OnChange(func(_ context.Context, key string, obs zsensor.Observation) {
		content := obs.Value
		if obs.Detail != "" {
			content = obs.Detail
		}
		notifications.Push(znotify.Notification{
			ToolName:  "sensor:" + key,
			Content:   content,
			Broadcast: true,
		})
		slog.Info("sensor change", "key", key, "value", obs.Value)
	})

	return runner
}
