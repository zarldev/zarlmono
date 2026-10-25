// Command replay-fixture exports the native replay's frames for offline UI
// development. It never loads a workspace or connects to an agent provider.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zarlcode/desktop/replay"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	service, err := replay.New()
	if err != nil {
		return err
	}
	first, err := service.Frame(0)
	if err != nil {
		return err
	}
	frames := make([]replay.Frame, 0, first.Total)
	for index := range first.Total {
		frame, frameErr := service.Frame(index)
		if frameErr != nil {
			return frameErr
		}
		frames = append(frames, frame)
	}
	if err := json.NewEncoder(os.Stdout).Encode(frames); err != nil {
		return fmt.Errorf("encode replay fixture: %w", err)
	}
	return nil
}
