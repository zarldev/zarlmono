package db_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestStoreReadersProgressWhileWriterHeld(t *testing.T) {
	// Subtests share a held writer and run before its commit.
	store, checkpoint := historyStore(t)
	if err := store.SetSetting(t.Context(), "/workspace", "pool-test", "committed"); err != nil {
		t.Fatal(err)
	}
	writer, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err := writer.ExecContext(t.Context(), "UPDATE settings SET value = 'uncommitted' WHERE key = 'pool-test'"); err != nil {
		t.Fatal(err)
	}

	// The writer holds both its sole pool connection and SQLite's write lock.
	// Every public read family must still run, including multi-query snapshots.
	for _, tt := range []struct {
		name string
		read func(context.Context) error
		want error
	}{
		{"setting", func(ctx context.Context) error {
			got, err := store.GetSetting(ctx, "/workspace", "pool-test")
			if err == nil && got != "committed" {
				return fmt.Errorf("read uncommitted value %q", got)
			}
			return err
		}, nil},
		{"exact setting", func(ctx context.Context) error {
			_, err := store.GetSettingExact(ctx, "/workspace", "pool-test")
			return err
		}, nil},
		{"effective settings", func(ctx context.Context) error { _, err := store.EffectiveSettings(ctx, "/workspace"); return err }, nil},
		{"api key", func(ctx context.Context) error { _, err := store.GetAPIKey(ctx, "/workspace", "missing"); return err }, db.ErrNotFound},
		{"exact api key", func(ctx context.Context) error {
			_, err := store.GetAPIKeyExact(ctx, "/workspace", "missing")
			return err
		}, db.ErrNotFound},
		{"all api keys", func(ctx context.Context) error { _, err := store.AllAPIKeys(ctx); return err }, nil},
		{"api key providers", func(ctx context.Context) error { _, err := store.ListAPIKeyProviders(ctx, "/workspace"); return err }, nil},
		{"dynamic tools", func(ctx context.Context) error { _, err := store.ListDynamicTools(ctx, "/workspace"); return err }, nil},
		{"mcp servers", func(ctx context.Context) error { _, err := store.ListMCPServers(ctx); return err }, nil},
		{"providers", func(ctx context.Context) error { _, err := store.ListProviders(ctx); return err }, nil},
		{"provider", func(ctx context.Context) error { _, err := store.GetProvider(ctx, "missing"); return err }, db.ErrNotFound},
		{"headless run", func(ctx context.Context) error { _, err := store.GetHeadlessRun(ctx, "missing"); return err }, db.ErrNotFound},
		{"headless runs", func(ctx context.Context) error {
			_, err := store.ListHeadlessRunsByWorkspace(ctx, "/workspace", 10)
			return err
		}, nil},
		{"headless attempts", func(ctx context.Context) error { _, err := store.ListHeadlessAttempts(ctx, "missing"); return err }, nil},
		{"verifier results", func(ctx context.Context) error {
			_, err := store.ListHeadlessVerifierResults(ctx, "missing")
			return err
		}, nil},
		{"session", func(ctx context.Context) error { _, err := store.GetSession(ctx, "source"); return err }, nil},
		{"sessions", func(ctx context.Context) error { _, err := store.ListSessions(ctx, "/workspace"); return err }, nil},
		{"session summaries", func(ctx context.Context) error { _, err := store.ListSessionSummaries(ctx, "/workspace"); return err }, nil},
		{"session version", func(ctx context.Context) error { _, err := store.SessionVersion(ctx, "source"); return err }, nil},
		{"branch", func(ctx context.Context) error { _, err := store.GetSessionBranch(ctx, "source"); return err }, db.ErrNotFound},
		{"tool output", func(ctx context.Context) error { _, err := store.GetToolOutput(ctx, "source", "missing"); return err }, db.ErrNotFound},
		{"tool outputs", func(ctx context.Context) error { _, err := store.ListToolOutputsBySession(ctx, "source"); return err }, nil},
		{"tool summaries", func(ctx context.Context) error {
			_, err := store.ListToolOutputSummariesBySession(ctx, "source")
			return err
		}, nil},
		{"tool history", func(ctx context.Context) error { _, err := store.ListToolOutputHistory(ctx, "source"); return err }, nil},
		{"model context", func(ctx context.Context) error { _, err := store.GetSessionModelContext(ctx, "source"); return err }, db.ErrNotFound},
		{"transcript snapshot", func(ctx context.Context) error { _, err := store.GetSessionTranscript(ctx, "source"); return err }, nil},
		{"resume snapshot", func(ctx context.Context) error { _, err := store.GetSessionResumeState(ctx, "source"); return err }, nil},
		{"checkpoint snapshot", func(ctx context.Context) error {
			_, err := store.GetSessionCheckpoint(ctx, "source", checkpoint.ID)
			return err
		}, nil},
		{"checkpoint list", func(ctx context.Context) error { _, err := store.ListSessionCheckpoints(ctx, "source"); return err }, nil},
		{"replay snapshot", func(ctx context.Context) error { _, err := store.ReadSessionReplay(ctx, "source"); return err }, nil},
		{"checkpoint history", func(ctx context.Context) error {
			_, _, _, err := store.ReadCheckpointHistory(ctx, checkpoint.History)
			return err
		}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := tt.read(ctx); !errors.Is(err, tt.want) {
				t.Fatalf("read with writer held: %v, want %v", err, tt.want)
			}
		})
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSetting(t.Context(), "/workspace", "pool-test"); err != nil || got != "uncommitted" {
		t.Fatalf("read after commit = %q, %v", got, err)
	}
}

func TestStoreTransactionReadsStayWithWriter(t *testing.T) {
	t.Parallel()
	store, checkpoint := historyStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rollback := errors.New("rollback test transaction")
	for _, outcome := range []error{rollback, nil} {
		err := store.WithTx(ctx, func(tx *db.Store) error {
			if err := tx.SetSetting(ctx, "/workspace", "transaction", "pending"); err != nil {
				return err
			}
			got, err := tx.GetSetting(ctx, "/workspace", "transaction")
			if err != nil {
				return fmt.Errorf("transaction read: %w", err)
			}
			if got != "pending" {
				return fmt.Errorf("transaction read = %q, want pending", got)
			}
			if err := tx.RenameSession(ctx, "source", "pending label"); err != nil {
				return err
			}
			state, err := tx.GetSessionResumeState(ctx, "source")
			if err != nil {
				return fmt.Errorf("transaction resume: %w", err)
			}
			if state.Session.Label != "pending label" {
				return fmt.Errorf("transaction resume = %q, want pending label", state.Session.Label)
			}
			if _, err := tx.GetSessionCheckpoint(ctx, "source", checkpoint.ID); err != nil {
				return err
			}
			outside, err := store.GetSessionResumeState(ctx, "source")
			if err != nil {
				return fmt.Errorf("outside resume: %w", err)
			}
			if outside.Session.Label == "pending label" {
				return errors.New("outside resume saw uncommitted label")
			}
			if state.ContentVersion == outside.ContentVersion {
				return errors.New("transaction version escaped to reader pool")
			}
			return outcome
		})
		if !errors.Is(err, outcome) {
			t.Fatalf("transaction: %v, want %v", err, outcome)
		}
		got, err := store.GetSetting(ctx, "/workspace", "transaction")
		if outcome != nil {
			if !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("rolled back value visible: %q, %v", got, err)
			}
		} else if err != nil || got != "pending" {
			t.Fatalf("committed value = %q, %v", got, err)
		}
	}
}

func TestStoreReaderPoolBoundAndReadOnly(t *testing.T) {
	t.Parallel()
	store := openTempStore(t)
	limit := store.ReadDB().Stats().MaxOpenConnections
	if limit != 4 {
		t.Fatalf("reader limit = %d, want 4", limit)
	}
	// Hold every connection to exercise initialization of more than the first.
	for range limit {
		conn, err := store.ReadDB().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		var journal string
		if err := conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
			t.Fatalf("reader journal = %q, %v", journal, err)
		}
		for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 30_000} {
			var got int
			if err := conn.QueryRowContext(t.Context(), "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
				t.Fatalf("reader %s = %d, %v; want %d", pragma, got, err, want)
			}
		}
		_, err = conn.ExecContext(t.Context(), "DELETE FROM settings")
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_READONLY {
			t.Fatalf("reader write error = %v, want SQLITE_READONLY", err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if _, err := store.GetSetting(ctx, "", "missing"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("saturated reader pool = %v, want deadline exceeded", err)
	}
	// Saturated readers must not prevent writes, including transaction reads.
	writeCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := store.SaveActiveSession(writeCtx, db.SessionRecord{ID: "independent-writer", Workspace: "/workspace"}); err != nil {
		t.Fatalf("write with saturated readers: %v", err)
	}
}

func TestStoreQueuedWriterCancellation(t *testing.T) {
	t.Parallel()
	store := openTempStore(t)
	writer, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if err := store.SetSetting(ctx, "", "cancelled", "never saved"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued write = %v, want deadline exceeded", err)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSetting(t.Context(), "", "cancelled"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cancelled write persisted: %v", err)
	}
	if err := store.SetSetting(t.Context(), "", "after-cancel", "saved"); err != nil {
		t.Fatalf("writer unavailable after cancellation: %v", err)
	}
}

func TestStoreCloseReleasesBothPools(t *testing.T) {
	t.Parallel()
	store := openTempStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().PingContext(t.Context()); err == nil {
		t.Error("writer remains open")
	}
	if err := store.ReadDB().PingContext(t.Context()); err == nil {
		t.Error("reader remains open")
	}
}

func TestStorePoolsUseSameEscapedPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state #1?mode=ro&value=100%.db")
	store, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetSetting(t.Context(), "", "escaped-path", "saved"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSetting(t.Context(), "", "escaped-path"); err != nil || got != "saved" {
		t.Fatalf("reader saw %q, %v", got, err)
	}
}
