package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func (p Launch) loadRateCard(ctx context.Context) (*engine.RateCardConfig, error) {
	if p.RateCardFile == "" {
		return nil, nil //nolint:nilnil // Nil is the valid absence of an optional explicit policy, not an invalid configuration.
	}
	config, err := loadRateCardFile(ctx, p.RateCardFile)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// loadRateCardFile follows ordinary user-provided OS path semantics, including
// symlinks to regular files. It performs no path expansion or normalization.
// Check before open as well as on the opened descriptor; nonblocking open also
// prevents a path replaced with a FIFO from hanging application startup.
func loadRateCardFile(ctx context.Context, path string) (engine.RateCardConfig, error) {
	invalid := func(detail string) (engine.RateCardConfig, error) {
		// Neither filesystem errors (which retain paths) nor raw parser causes
		// cross this boundary. detail is always static repository-owned text.
		return engine.RateCardConfig{}, fmt.Errorf("%w: %s", engine.ErrInvalidRateCardConfig, detail)
	}
	if err := ctx.Err(); err != nil {
		return engine.RateCardConfig{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return invalid("inspect local file")
	}
	if !info.Mode().IsRegular() {
		return invalid("regular local file required")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return invalid("open local file")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return invalid("regular local file required")
	}
	if info.Size() > engine.MaxRateCardConfigBytes {
		return invalid("local file exceeds size limit")
	}
	if err := ctx.Err(); err != nil {
		return engine.RateCardConfig{}, err
	}
	data, err := io.ReadAll(io.LimitReader(file, engine.MaxRateCardConfigBytes+1))
	if err != nil {
		return invalid("read local file")
	}
	if err := ctx.Err(); err != nil {
		return engine.RateCardConfig{}, err
	}
	return engine.ParseRateCardConfig(data, time.Now())
}
