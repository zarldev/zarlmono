package tuismoke_test

import (
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/tools/tuismoke"
)

func TestVerifyCredentialStorage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		ciphertext []byte
		storage    string
		version    int
		wantError  bool
	}{
		{"current ciphertext", []byte{1, 2, 3}, "vault", 2, false},
		{"plaintext storage", []byte("smoke-secret"), "plaintext", 2, true},
		{"plaintext mislabeled", []byte("smoke-secret"), "vault", 2, true},
		{"unsupported version", []byte{1, 2, 3}, "vault", 1, true},
		{"empty ciphertext", []byte{}, "vault", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "state.db")
			u := url.URL{Scheme: "file", Path: path}
			store, err := sql.Open("sqlite3", u.String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			_, err = store.ExecContext(t.Context(), "CREATE TABLE api_keys (workspace TEXT, provider TEXT, ciphertext BLOB, storage TEXT, key_version INTEGER)")
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.ExecContext(t.Context(), "INSERT INTO api_keys VALUES ('', 'openai', ?, ?, ?)", tc.ciphertext, tc.storage, tc.version)
			if err != nil {
				t.Fatal(err)
			}
			err = tuismoke.VerifyCredentialStorage(t.Context(), path)
			if (err != nil) != tc.wantError {
				t.Fatalf("VerifyCredentialStorage error = %v, want error %v", err, tc.wantError)
			}
		})
	}
}

func TestVerifyMissingDatabaseDoesNotCreateIt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing.db")
	if err := tuismoke.VerifyCredentialStorage(t.Context(), path); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("verification created a database: %v", err)
	}
}

func TestRunRejectsUnboundedTimeout(t *testing.T) {
	t.Parallel()
	if err := tuismoke.Run(t.Context(), ".", "", 0, io.Discard); err == nil {
		t.Fatal("unbounded timeout accepted")
	}
}
