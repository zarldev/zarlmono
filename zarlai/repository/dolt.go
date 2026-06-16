package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

const defaultDSN = "root:@tcp(localhost:3307)/zarl?parseTime=true"

func NewDB(dsn string) (*sql.DB, error) {
	if dsn == "" {
		dsn = defaultDSN
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	// Bounded ping: a dead or half-open Dolt (port accepting, handshake
	// stalled) must fail fast — at server boot and in tests that skip
	// when the database is down — not block indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

// DoltCommit represents a Dolt version control commit.
type DoltCommit struct {
	Hash      string
	Committer string
	Message   string
	Date      string
}

// DoltDiff represents a diff entry between two commits.
type DoltDiff struct {
	DiffType    string
	FromContent string
	ToContent   string
}

// DoltRepo provides Dolt version control operations.
type DoltRepo struct {
	db *sql.DB
}

// NewDoltRepo creates a DoltRepo.
func NewDoltRepo(db *sql.DB) *DoltRepo {
	return &DoltRepo{db: db}
}

// Commit creates a Dolt commit with the given message.
func (r *DoltRepo) Commit(ctx context.Context, message string) error {
	_, err := r.db.ExecContext(ctx, "CALL dolt_add('-A')")
	if err != nil {
		return fmt.Errorf("dolt add: %w", err)
	}
	_, err = r.db.ExecContext(ctx, "CALL dolt_commit('-m', ?)", message)
	if err != nil {
		return fmt.Errorf("dolt commit: %w", err)
	}
	return nil
}

// Log returns recent Dolt commits.
func (r *DoltRepo) Log(ctx context.Context, limit int) ([]DoltCommit, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT commit_hash, committer, message, `date` FROM dolt_log ORDER BY `date` DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("dolt log: %w", err)
	}
	defer rows.Close()

	var commits []DoltCommit
	for rows.Next() {
		var c DoltCommit
		var date time.Time
		if err := rows.Scan(&c.Hash, &c.Committer, &c.Message, &date); err != nil {
			return nil, fmt.Errorf("scan dolt log: %w", err)
		}
		c.Date = date.Format(timeFormat)
		commits = append(commits, c)
	}
	return commits, rows.Err()
}

// DiffPrompts returns prompt diffs between two commits.
func (r *DoltRepo) DiffPrompts(ctx context.Context, fromHash, toHash string) ([]DoltDiff, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT diff_type, from_content, to_content FROM dolt_diff_prompts WHERE from_commit = ? AND to_commit = ?",
		fromHash, toHash,
	)
	if err != nil {
		return nil, fmt.Errorf("dolt diff prompts: %w", err)
	}
	defer rows.Close()

	var diffs []DoltDiff
	for rows.Next() {
		var d DoltDiff
		var fromContent, toContent sql.NullString
		if err := rows.Scan(&d.DiffType, &fromContent, &toContent); err != nil {
			return nil, fmt.Errorf("scan dolt diff: %w", err)
		}
		d.FromContent = fromContent.String
		d.ToContent = toContent.String
		diffs = append(diffs, d)
	}
	return diffs, rows.Err()
}

// RevertPrompt reads a prompt's content from a previous commit.
func (r *DoltRepo) RevertPrompt(ctx context.Context, commitHash, promptID string) (string, error) {
	var content string
	err := r.db.QueryRowContext(ctx,
		"SELECT content FROM prompts AS OF ? WHERE id = ?",
		commitHash, promptID,
	).Scan(&content)
	if err != nil {
		return "", fmt.Errorf("read prompt at commit: %w", err)
	}
	return content, nil
}
