package vault_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/vault"
)

func TestVaultRejectsInvalidKDFBeforePrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := vault.Open(dir, fixedPass("valid")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "master.kdf")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var valid map[string]json.RawMessage
	if err := json.Unmarshal(original, &valid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, field string
		value       json.RawMessage
	}{
		{"zero time", "time", json.RawMessage(`0`)},
		{"excessive time", "time", json.RawMessage(`4294967295`)},
		{"zero memory", "memory", json.RawMessage(`0`)},
		{"excessive memory", "memory", json.RawMessage(`4294967295`)},
		{"zero threads", "threads", json.RawMessage(`0`)},
		{"excessive threads", "threads", json.RawMessage(`255`)},
		{"key length", "key_length", json.RawMessage(`4294967295`)},
		{"salt", "salt", json.RawMessage(`""`)},
		{"nonce", "verifier_nonce", json.RawMessage(`""`)},
		{"verifier", "verifier", json.RawMessage(`""`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields := maps.Clone(valid)
			fields[tc.field] = tc.value
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			assertInvalidKDF(t, filepath.Join(t.TempDir(), "master.kdf"), data)
		})
	}
	for name, data := range map[string][]byte{
		"missing fields": []byte(`{}`),
		"invalid JSON":   []byte(`{`),
		"oversized":      bytes.Repeat([]byte(" "), 64*1024+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertInvalidKDF(t, filepath.Join(t.TempDir(), "master.kdf"), data)
		})
	}
}

func assertInvalidKDF(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := vault.Open(filepath.Dir(path), func(bool, bool) (string, error) {
		t.Error("prompted before rejecting invalid storage metadata")
		return "valid", nil
	})
	if !errors.Is(err, vault.ErrInvalidKDF) {
		t.Fatalf("Open error = %v, want ErrInvalidKDF", err)
	}
}
