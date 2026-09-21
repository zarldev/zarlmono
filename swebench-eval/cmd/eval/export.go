package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/evalconfig"
	"github.com/zarldev/zarlmono/swebench-eval/report"
)

func exportRun(cfg evalconfig.PersistenceConfig) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	path := cfg.DBPath
	if path == "" {
		var err error
		path, err = db.DefaultPath()
		if err != nil {
			return err
		}
	}
	// Export must not create a new empty database when the selected file is absent.
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("open evaluation database for export: %w", err)
	}
	store, err := db.Open(ctx, path)
	if err != nil {
		return err
	}
	defer store.Close()
	snapshot, err := store.ReadRunSnapshot(ctx, cfg.ExportRun)
	if err != nil {
		return err
	}
	return report.JSON(os.Stdout, snapshot)
}
