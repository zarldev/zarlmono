// Package vault provides at-rest encryption for zarlcode credentials.
// It owns the AES-256-GCM primitive and the master-key lifecycle; the
// master key never leaves the process, only ciphertext + nonce reach
// disk. Composing the vault with the db.Store to persist/fetch
// credentials is prefs.Service's job, not the vault's.
//
// The master key is derived from a passphrase via Argon2id (the KDF
// parameters and a random salt live in ~/.zarlcode/master.kdf, alongside
// a verifier blob used to detect a wrong passphrase). Two non-interactive
// overrides skip the prompt: $ZARLCODE_KEY (a raw base64 32-byte key)
// and $ZARLCODE_PASSPHRASE (a passphrase fed to the same KDF). A legacy
// random key at ~/.zarlcode/master.key (the pre-passphrase scheme) is kept
// as a decrypt fallback so prefs can migrate old rows, then removed.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

const (
	// masterKeyEnv is a raw base64-encoded 32-byte key — the strongest,
	// fully non-interactive override (no passphrase, no KDF).
	masterKeyEnv = "ZARLCODE_KEY"
	// masterPassphraseEnv supplies the passphrase non-interactively (headless
	// / eval); it goes through the same Argon2id KDF as the interactive prompt.
	masterPassphraseEnv = "ZARLCODE_PASSPHRASE"

	legacyKeyFileRelPath = "master.key" // pre-passphrase random key
	kdfFileRelPath       = "master.kdf" // salt + KDF params + verifier
	masterKeySize        = 32           // AES-256

	maxPassphraseAttempts = 3
)

// CurrentKeyVersion is the key-rotation version stamped onto ciphertext the
// vault writes. v1 was the random ~/.zarlcode/master.key; v2 is the Argon2id
// passphrase-derived key. Exported so prefs.Service can record it and detect
// rows still on the old key.
const CurrentKeyVersion = 2

// verifierPlaintext is encrypted under a freshly-derived key and stored in the
// KDF file; decrypting it on a later open confirms the entered passphrase is
// correct before any real credential is touched.
const verifierPlaintext = "zarlcode-vault-verifier-v2"

// Default Argon2id cost. Persisted per-vault in the KDF file so raising these
// later doesn't strand existing vaults.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
)

var (
	// ErrUninitialised means no vault exists yet and no way to create one was
	// provided (no env override, no interactive prompt). The caller degrades
	// to "credentials disabled" rather than failing the launch.
	ErrUninitialised = errors.New("vault: not initialised")
	// ErrLocked means a vault exists but no passphrase source was available to
	// unlock it (e.g. a headless run with neither env var set).
	ErrLocked = errors.New("vault: locked (no passphrase source)")
	// ErrWrongPassphrase is returned after the interactive attempt budget is
	// exhausted, or immediately when the env passphrase is wrong.
	ErrWrongPassphrase = errors.New("vault: wrong passphrase")
)

// PassphraseFunc supplies the master passphrase interactively. setup is true on
// first-ever use (no KDF file yet) so the caller can confirm a new passphrase;
// retry is true when a previous attempt was wrong. Returning an error aborts
// the unlock (e.g. the user pressed Ctrl-C at the prompt).
type PassphraseFunc func(setup, retry bool) (string, error)

// Vault wraps the AEAD primitive used to encrypt API keys at rest. The master
// key never leaves this process. legacy is non-nil while a pre-passphrase
// master.key is still on disk, so old ciphertext keeps decrypting until
// prefs.Service migrates it.
type Vault struct {
	primary    cipher.AEAD
	legacy     cipher.AEAD
	legacyPath string
}

// kdfFile is the on-disk KDF material: a random salt, the Argon2id cost it was
// derived with, and a verifier blob (verifierPlaintext sealed under the key).
type kdfFile struct {
	Salt      []byte `json:"salt"`
	Time      uint32 `json:"time"`
	Memory    uint32 `json:"memory"`
	Threads   uint8  `json:"threads"`
	VNonce    []byte `json:"verifier_nonce"`
	Verifier  []byte `json:"verifier"`
	KeyLength uint32 `json:"key_length"`
}

// Exists reports whether a vault has been initialised on disk — a KDF file
// (passphrase scheme) or a legacy master.key. Callers use it to decide whether
// to prompt for a passphrase at startup: a fresh install with no stored
// credentials (the local-llama.cpp default) shouldn't be nagged. $ZARLCODE_KEY
// / $ZARLCODE_PASSPHRASE callers don't need this — Open handles them directly.
func Exists() (bool, error) {
	dir, err := db.DefaultDir()
	if err != nil {
		return false, err
	}
	for _, name := range []string{kdfFileRelPath, legacyKeyFileRelPath} {
		switch _, err := os.Stat(filepath.Join(dir, name)); {
		case err == nil:
			return true, nil
		case errors.Is(err, fs.ErrNotExist):
			// keep checking
		default:
			return false, fmt.Errorf("stat %s: %w", name, err)
		}
	}
	return false, nil
}

// Open loads or initialises the master key and constructs the vault.
// Precedence: $ZARLCODE_KEY (raw key) → $ZARLCODE_PASSPHRASE (KDF) →
// interactive passphrase. A nil passphrase func with neither env var set and
// no existing vault returns ErrUninitialised; with an existing vault it
// returns ErrLocked.
func Open(passphrase PassphraseFunc) (*Vault, error) {
	dir, err := db.DefaultDir()
	if err != nil {
		return nil, err
	}
	legacyPath := filepath.Join(dir, legacyKeyFileRelPath)
	legacyAEAD, _, err := loadLegacy(legacyPath)
	if err != nil {
		return nil, err
	}

	// 1. Explicit raw key — no KDF, no prompt.
	if v := os.Getenv(masterKeyEnv); v != "" {
		key, err := decodeRawKey(v)
		if err != nil {
			return nil, err
		}
		return newVault(key, legacyAEAD, legacyPath)
	}

	// 2. Passphrase-derived. Load the KDF material if a vault already exists.
	if err := os.MkdirAll(dir, filesystem.ModePrivateDir); err != nil {
		return nil, fmt.Errorf("vault dir: %w", err)
	}
	kdfPath := filepath.Join(dir, kdfFileRelPath)
	kdf, kdfExists, err := loadKDF(kdfPath)
	if err != nil {
		return nil, err
	}

	envPass := os.Getenv(masterPassphraseEnv)
	if envPass == "" && passphrase == nil {
		// No way to obtain a passphrase.
		if kdfExists || legacyAEAD != nil {
			return nil, ErrLocked
		}
		return nil, ErrUninitialised
	}

	// Env passphrase: one shot, no retry.
	if envPass != "" {
		key, ok, err := deriveOrInit(envPass, &kdf, kdfExists, kdfPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrWrongPassphrase
		}
		return newVault(key, legacyAEAD, legacyPath)
	}

	// Interactive: prompt, with a small retry budget on a wrong passphrase.
	setup := !kdfExists
	for attempt := range maxPassphraseAttempts {
		pass, perr := passphrase(setup, attempt > 0)
		if perr != nil {
			return nil, fmt.Errorf("vault passphrase: %w", perr)
		}
		key, ok, derr := deriveOrInit(pass, &kdf, kdfExists, kdfPath)
		if derr != nil {
			return nil, derr
		}
		if ok {
			return newVault(key, legacyAEAD, legacyPath)
		}
	}
	return nil, ErrWrongPassphrase
}

// deriveOrInit derives the master key from pass. When the KDF file doesn't
// exist yet (setup), it generates a salt, seals the verifier, and persists the
// KDF file atomically — always returning ok=true. Otherwise it derives against
// the stored salt/params and reports ok=false when the verifier fails to open
// (wrong passphrase). A non-nil error is an I/O fault, not a wrong passphrase.
func deriveOrInit(pass string, kdf *kdfFile, exists bool, kdfPath string) ([]byte, bool, error) {
	if !exists {
		salt := make([]byte, masterKeySize)
		if _, err := rand.Read(salt); err != nil {
			return nil, false, fmt.Errorf("vault salt: %w", err)
		}
		key := argon2.IDKey([]byte(pass), salt, argonTime, argonMemory, argonThreads, masterKeySize)
		aead, err := aeadFromKey(key)
		if err != nil {
			return nil, false, err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, false, fmt.Errorf("vault verifier nonce: %w", err)
		}
		f := kdfFile{
			Salt:      salt,
			Time:      argonTime,
			Memory:    argonMemory,
			Threads:   argonThreads,
			KeyLength: masterKeySize,
			VNonce:    nonce,
			Verifier:  aead.Seal(nil, nonce, []byte(verifierPlaintext), nil),
		}
		blob, err := json.Marshal(f)
		if err != nil {
			return nil, false, fmt.Errorf("vault kdf encode: %w", err)
		}
		if err := writeFileAtomic(kdfPath, blob, filesystem.ModePrivateFile); err != nil {
			return nil, false, err
		}
		return key, true, nil
	}

	derived := argon2.IDKey([]byte(pass), kdf.Salt, kdf.Time, kdf.Memory, kdf.Threads, kdf.KeyLength)
	aead, err := aeadFromKey(derived)
	if err != nil {
		return nil, false, err
	}
	if _, oerr := aead.Open(nil, kdf.VNonce, kdf.Verifier, nil); oerr == nil {
		return derived, true, nil
	}
	return nil, false, nil // wrong passphrase — ok=false, not an error
}

func newVault(key []byte, legacy cipher.AEAD, legacyPath string) (*Vault, error) {
	primary, err := aeadFromKey(key)
	if err != nil {
		return nil, err
	}
	return &Vault{primary: primary, legacy: legacy, legacyPath: legacyPath}, nil
}

func aeadFromKey(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault gcm: %w", err)
	}
	return aead, nil
}

func decodeRawKey(v string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%s: not valid base64: %w", masterKeyEnv, err)
	}
	if len(key) != masterKeySize {
		return nil, fmt.Errorf("%s: decoded to %d bytes, want %d", masterKeyEnv, len(key), masterKeySize)
	}
	return key, nil
}

// loadKDF reads the KDF file. Returns exists=false (and no error) when the file
// is absent — the first-run setup path.
func loadKDF(path string) (kdfFile, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return kdfFile{}, false, nil
	}
	if err != nil {
		return kdfFile{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	var f kdfFile
	if err := json.Unmarshal(data, &f); err != nil {
		return kdfFile{}, false, fmt.Errorf("decode %s: %w", path, err)
	}
	return f, true, nil
}

// loadLegacy builds an AEAD from a pre-passphrase ~/.zarlcode/master.key when
// present, so old ciphertext keeps decrypting until prefs migrates it. A
// wrong-length file is fatal (won't silently ignore credentials it can't read).
func loadLegacy(path string) (cipher.AEAD, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) != masterKeySize {
		return nil, false, fmt.Errorf("%s: %d bytes, want %d (legacy master key corrupt)", path, len(data), masterKeySize)
	}
	aead, err := aeadFromKey(data)
	if err != nil {
		return nil, false, err
	}
	return aead, true, nil
}

// Encrypt returns ciphertext + nonce for plaintext, under the primary
// (current) key. AES-GCM nonces must be unique under the same key — a fresh
// random 12-byte nonce per call ensures callers never have to think about reuse.
func (v *Vault) Encrypt(plaintext string) ([]byte, []byte, error) {
	nc := make([]byte, v.primary.NonceSize())
	if _, err := rand.Read(nc); err != nil {
		return nil, nil, fmt.Errorf("nonce: %w", err)
	}
	ciphertext := v.primary.Seal(nil, nc, []byte(plaintext), nil)
	return ciphertext, nc, nil
}

// Decrypt reverses Encrypt. It tries the primary key, then the legacy key when
// one is still present, so rows written under the old master.key keep
// decrypting through the migration window.
//
// A wrong-length nonce is reported as a decrypt error rather than reaching
// GCM.Open, which panics on a mismatched nonce length. A malformed stored row
// (e.g. an empty nonce on a row flagged as encrypted) must surface as an error
// the caller can wrap, not crash the process.
func (v *Vault) Decrypt(ciphertext, nonce []byte) (string, error) {
	if len(nonce) != v.primary.NonceSize() {
		return "", fmt.Errorf("decrypt: nonce is %d bytes, want %d (stored row malformed)", len(nonce), v.primary.NonceSize())
	}
	if plain, err := v.primary.Open(nil, nonce, ciphertext, nil); err == nil {
		return string(plain), nil
	}
	if v.legacy != nil {
		if plain, err := v.legacy.Open(nil, nonce, ciphertext, nil); err == nil {
			return string(plain), nil
		}
	}
	return "", errors.New("decrypt: authentication failed (key changed or ciphertext corrupt)")
}

// HasLegacy reports whether a pre-passphrase master.key is still present (so
// prefs knows it has rows to migrate).
func (v *Vault) HasLegacy() bool { return v.legacy != nil }

// RemoveLegacy deletes the legacy master.key and drops the in-memory legacy
// key. Called by prefs.Service ONLY after every row has been re-encrypted
// under the primary key, so nothing becomes unreadable.
func (v *Vault) RemoveLegacy() error {
	if v.legacy == nil {
		return nil
	}
	if err := os.Remove(v.legacyPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove legacy master key: %w", err)
	}
	v.legacy = nil
	return nil
}

// writeFileAtomic writes data to a temp file in the same directory, fsyncs it,
// and renames it into place — so a crash mid-write can't leave a truncated KDF
// file (which would make every stored credential undecryptable).
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vault-*")
	if err != nil {
		return fmt.Errorf("vault temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vault chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vault write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vault sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("vault close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("vault rename: %w", err)
	}
	return nil
}

// Credential persistence (encrypt→store, fetch→decrypt) lives in
// prefs.Service, the single orchestrator that composes this vault with
// the db.Store. The vault stays focused on the crypto primitives
// (Encrypt / Decrypt) plus master-key management.
