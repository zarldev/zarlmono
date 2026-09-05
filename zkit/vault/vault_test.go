package vault_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/vault"
)

func fixedPass(p string) vault.PassphraseFunc {
	return func(_, _ bool) (string, error) { return p, nil }
}

func TestVault_PassphraseRoundTrip(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "credentials")
	v, err := vault.Open(dir, fixedPass("hunter2"))
	if err != nil {
		t.Fatalf("setup open: %v", err)
	}
	ct, nonce, err := v.Encrypt("sk-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	v2, err := vault.Open(dir, fixedPass("hunter2"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := v2.Decrypt(ct, nonce)
	if err != nil || got != "sk-secret" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	for path, want := range map[string]os.FileMode{
		dir: 0o700, filepath.Join(dir, "master.kdf"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

// TestVault_ConcurrentFirstOpen verifies every concurrent opener derives the
// key installed by the single atomic initialiser.
func TestVault_ConcurrentFirstOpen(t *testing.T) {
	dir := t.TempDir()
	start := make(chan struct{})
	results := make(chan *vault.Vault, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			v, err := vault.Open(dir, fixedPass("same-passphrase"))
			if err != nil {
				errs <- err
				return
			}
			results <- v
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	values := make([]*vault.Vault, 0, 2)
	for v := range results {
		values = append(values, v)
	}
	if len(values) != 2 {
		t.Fatalf("opened vaults = %d, want 2", len(values))
	}
	ciphertext, nonce, err := values[0].Encrypt("credential")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := values[1].Decrypt(ciphertext, nonce); err != nil || got != "credential" {
		t.Fatalf("second concurrent opener decrypt = %q, %v", got, err)
	}
}

func TestVault_WrongPassphrase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := vault.Open(dir, fixedPass("correct")); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(dir, fixedPass("incorrect")); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("Open(wrong) = %v, want ErrWrongPassphrase", err)
	}
}

func TestVault_DecryptWrongNonceLength(t *testing.T) {
	t.Parallel()
	v, err := vault.Open(t.TempDir(), fixedPass("pw"))
	if err != nil {
		t.Fatal(err)
	}
	ct, _, err := v.Encrypt("x")
	if err != nil {
		t.Fatal(err)
	}
	for _, nonce := range [][]byte{nil, {}, make([]byte, 11), make([]byte, 13)} {
		if _, err := v.Decrypt(ct, nonce); err == nil {
			t.Errorf("Decrypt(len=%d nonce) = nil err, want decrypt error", len(nonce))
		}
	}
}

func TestVault_ExistsAndLockStates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if ex, err := vault.Exists(dir); err != nil || ex {
		t.Fatalf("Exists on fresh = %v,%v, want false,nil", ex, err)
	}
	if _, err := vault.Open(dir, nil); !errors.Is(err, vault.ErrUninitialised) {
		t.Fatalf("Open(nil) fresh = %v, want ErrUninitialised", err)
	}
	if _, err := vault.Open(dir, fixedPass("p")); err != nil {
		t.Fatal(err)
	}
	if ex, err := vault.Exists(dir); err != nil || !ex {
		t.Fatalf("Exists after setup = %v,%v, want true,nil", ex, err)
	}
	if _, err := vault.Open(dir, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("Open(nil) existing = %v, want ErrLocked", err)
	}
}

func TestVaultIgnoresCredentialEnvironment(t *testing.T) {
	// Even valid old overrides cannot initialise, unlock, or override a vault.
	t.Setenv("ZARLCODE_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("ZARLCODE_PASSPHRASE", "explicit")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	if _, err := vault.Open(dir, nil); !errors.Is(err, vault.ErrUninitialised) {
		t.Fatalf("environment initialised vault: %v", err)
	}
	v, err := vault.Open(dir, fixedPass("explicit"))
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := v.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(dir, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("environment unlocked vault: %v", err)
	}
	t.Setenv("ZARLCODE_KEY", "not even base64")
	t.Setenv("ZARLCODE_PASSPHRASE", "wrong")
	reopened, err := vault.Open(dir, fixedPass("explicit"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.Decrypt(ct, nonce); err != nil || got != "secret" {
		t.Fatalf("environment changed encryption key: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zarlcode")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vault touched application home: %v", err)
	}
	if exists, err := vault.Exists(t.TempDir()); err != nil || exists {
		t.Fatalf("vault path leaked between directories: %v, %v", exists, err)
	}
}
