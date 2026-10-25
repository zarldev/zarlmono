// Binary swarm exercises scripted handoffs or an opt-in bounded live-model trial.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func main() {
	preflight := flag.Bool("live-preflight", false, "Check stored credentials for DeepSeek, Sol, and Luna without model calls")
	live := flag.Bool("live", false, "Run the bounded DeepSeek/Sol/Luna trial using stored credentials (makes live model calls)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	var err error
	switch {
	case *preflight:
		err = livePreflight(ctx)
	case *live:
		err = runLive(ctx)
	default:
		err = run(ctx)
	}
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	report, err := RunExperiment(ctx, ExperimentLimits(), runner.NopSink{})
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(report); encodeErr != nil {
		return fmt.Errorf("encode experiment report: %w", encodeErr)
	}
	return err
}

func runLive(ctx context.Context) error {
	agents, settings, err := openLiveProviders(ctx)
	if err != nil {
		return err
	}
	report, err := RunLiveExperiment(ctx, agents)
	closeErr := settings.Close()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return errors.Join(err, closeErr, encoder.Encode(report))
}
