package vault_test

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/vault"
)

// tmpHome points the vault at a throwaway ~/.zarlcode (db.DefaultDir resolves
// from $HOME) and clears the env overrides so each test starts uninitialised.
func tmpHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZARLCODE_KEY", "")
	t.Setenv("ZARLCODE_PASSPHRASE", "")
	return filepath.Join(home, ".zarlcode")
}

func fixedPass(p string) vault.PassphraseFunc {
	return func(_, _ bool) (string, error) { return p, nil }
}

func TestVault_PassphraseRoundTrip(t *testing.T) {
	dir := tmpHome(t)

	v, err := vault.Open(fixedPass("hunter2")) // first run: setup
	if err != nil {
		t.Fatalf("setup open: %v", err)
	}
	ct, nonce, err := v.Encrypt("sk-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Reopen with the same passphrase — must decrypt.
	v2, err := vault.Open(fixedPass("hunter2"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := v2.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != "sk-secret" {
		t.Errorf("decrypt = %q, want sk-secret", got)
	}

	// File permissions: KDF file 0600, home dir 0700.
	if info, err := os.Stat(filepath.Join(dir, "master.kdf")); err != nil {
		t.Fatalf("stat kdf: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("master.kdf mode = %o, want 600", perm)
	}
	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("stat dir: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("~/.zarlcode mode = %o, want 700", perm)
	}
}

func TestVault_WrongPassphrase(t *testing.T) {
	tmpHome(t)
	if _, err := vault.Open(fixedPass("correct")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := vault.Open(fixedPass("incorrect")) // retries exhausted
	if !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("Open(wrong) err = %v, want ErrWrongPassphrase", err)
	}
}

func TestVault_RawKeyEnv(t *testing.T) {
	tmpHome(t)
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	ct, nonce, _ := v.Encrypt("x")
	if got, err := v.Decrypt(ct, nonce); err != nil || got != "x" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
}

// A malformed stored row (here an empty nonce on material flagged as
// encrypted) must surface as a decrypt error, not panic GCM.Open and kill the
// process.
func TestVault_DecryptWrongNonceLength(t *testing.T) {
	tmpHome(t)
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	ct, _, _ := v.Encrypt("x")
	for _, nonce := range [][]byte{nil, {}, make([]byte, 11), make([]byte, 13)} {
		if _, err := v.Decrypt(ct, nonce); err == nil {
			t.Errorf("Decrypt(len=%d nonce) = nil err, want decrypt error", len(nonce))
		}
	}
}

func TestVault_EnvPassphrase(t *testing.T) {
	tmpHome(t)
	t.Setenv("ZARLCODE_PASSPHRASE", "envpass")
	v, err := vault.Open(nil) // setup via env, no prompt
	if err != nil {
		t.Fatalf("env setup: %v", err)
	}
	ct, nonce, _ := v.Encrypt("y")

	v2, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("env reopen: %v", err)
	}
	if got, err := v2.Decrypt(ct, nonce); err != nil || got != "y" {
		t.Errorf("round trip = %q, %v", got, err)
	}

	t.Setenv("ZARLCODE_PASSPHRASE", "wrong")
	if _, err := vault.Open(nil); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Errorf("wrong env passphrase err = %v, want ErrWrongPassphrase", err)
	}
}

func TestVault_ExistsAndLockStates(t *testing.T) {
	tmpHome(t)

	if ex, err := vault.Exists(); err != nil || ex {
		t.Fatalf("Exists on fresh = %v,%v, want false,nil", ex, err)
	}
	// No passphrase source + nothing on disk → uninitialised (degrade, don't fail).
	if _, err := vault.Open(nil); !errors.Is(err, vault.ErrUninitialised) {
		t.Fatalf("Open(nil) fresh err = %v, want ErrUninitialised", err)
	}

	if _, err := vault.Open(fixedPass("p")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if ex, err := vault.Exists(); err != nil || !ex {
		t.Fatalf("Exists after setup = %v,%v, want true,nil", ex, err)
	}
	// Vault exists but no passphrase source → locked.
	if _, err := vault.Open(nil); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("Open(nil) with vault present err = %v, want ErrLocked", err)
	}
}
