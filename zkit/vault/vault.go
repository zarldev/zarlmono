// Package vault provides at-rest credential encryption and master-key storage.
// Callers supply a storage directory and a passphrase source; vault does not
// resolve application settings, environment variables, or database locations.
//
// The master key is derived via Argon2id. Its salt, KDF parameters, and verifier
// live in master.kdf within the supplied directory. A legacy master.key remains
// available for decryption until the caller migrates stored ciphertext and
// explicitly removes it. Persisted formats are unchanged by path injection.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

const (
	legacyKeyFileRelPath = "master.key" // pre-passphrase random key
	kdfFileRelPath       = "master.kdf" // salt + KDF params + verifier
	masterKeySize        = 32           // AES-256

	maxPassphraseAttempts = 3
)

// CurrentKeyVersion identifies the key scheme stamped onto stored ciphertext:
// v1 used a random master.key; v2 uses an Argon2id passphrase-derived key.
const CurrentKeyVersion = 2

// verifierPlaintext is encrypted under a freshly-derived key and stored in the
// KDF file; decrypting it on a later open confirms the entered passphrase is
// correct before any real credential is touched.
// Its historical value is part of the persisted format and must not change.
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
	// provided (no passphrase source).
	ErrUninitialised = errors.New("vault: not initialised")
	// ErrLocked means a vault exists but no passphrase source was available to
	// unlock it, for example when a non-interactive caller passes nil.
	ErrLocked = errors.New("vault: locked (no passphrase source)")
	// ErrWrongPassphrase is returned after the interactive attempt budget is
	// exhausted. Errors from the supplied passphrase source abort immediately.
	ErrWrongPassphrase = errors.New("vault: wrong passphrase")
	// ErrNotFound means optional vault material does not exist on disk.
	ErrNotFound = errors.New("vault: material not found")
)

// PassphraseFunc explicitly supplies the master passphrase. setup is true on
// first-ever use (no KDF file yet) so the caller can confirm a new passphrase;
// retry is true when a previous attempt was wrong. Returning an error aborts
// the unlock (e.g. the user pressed Ctrl-C at the prompt).
type PassphraseFunc func(setup, retry bool) (string, error)

// Vault wraps the AEAD primitive used to encrypt API keys at rest. The master
// key never leaves this process. legacy is non-nil while a pre-passphrase
// master.key is still on disk, so old ciphertext keeps decrypting until
// the caller migrates it.
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

// Exists reports whether dir contains master.kdf or a legacy master.key.
func Exists(dir string) (bool, error) {
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

// Open loads or initialises a passphrase-derived master key in dir.
// A nil passphrase source returns ErrUninitialised if no vault exists, or
// ErrLocked if existing material requires unlocking. Open never reads secrets
// from the environment and does not choose a default directory.
func Open(dir string, passphrase PassphraseFunc) (*Vault, error) {
	legacyPath := filepath.Join(dir, legacyKeyFileRelPath)
	legacyAEAD, err := loadLegacy(legacyPath)
	if errors.Is(err, ErrNotFound) {
		legacyAEAD, err = nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Load KDF material from the explicitly selected vault directory.
	if err := os.MkdirAll(dir, filesystem.ModePrivateDir); err != nil {
		return nil, fmt.Errorf("vault dir: %w", err)
	}
	kdfPath := filepath.Join(dir, kdfFileRelPath)
	kdf, err := loadKDF(kdfPath)
	kdfExists := err == nil
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	if err != nil {
		return nil, err
	}

	if passphrase == nil {
		// No way to obtain a passphrase.
		if kdfExists || legacyAEAD != nil {
			return nil, ErrLocked
		}
		return nil, ErrUninitialised
	}

	// Interactive: prompt, with a small retry budget on a wrong passphrase.
	setup := !kdfExists
	for attempt := range maxPassphraseAttempts {
		pass, perr := passphrase(setup, attempt > 0)
		if perr != nil {
			return nil, fmt.Errorf("vault passphrase: %w", perr)
		}
		key, derr := deriveOrInit(pass, &kdf, kdfExists, kdfPath)
		if derr == nil {
			return newVault(key, legacyAEAD, legacyPath)
		}
		if !errors.Is(derr, ErrWrongPassphrase) {
			return nil, derr
		}
	}
	return nil, ErrWrongPassphrase
}

// deriveOrInit derives or creates the master key. A wrong verifier returns
// ErrWrongPassphrase; all other errors are operational failures.
func deriveOrInit(pass string, kdf *kdfFile, exists bool, kdfPath string) ([]byte, error) {
	if !exists {
		salt := make([]byte, masterKeySize)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("vault salt: %w", err)
		}
		key := argon2.IDKey([]byte(pass), salt, argonTime, argonMemory, argonThreads, masterKeySize)
		aead, err := aeadFromKey(key)
		if err != nil {
			return nil, err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, fmt.Errorf("vault verifier nonce: %w", err)
		}
		f := kdfFile{Salt: salt, Time: argonTime, Memory: argonMemory, Threads: argonThreads, KeyLength: masterKeySize, VNonce: nonce, Verifier: aead.Seal(nil, nonce, []byte(verifierPlaintext), nil)}
		blob, err := json.Marshal(f)
		if err != nil {
			return nil, fmt.Errorf("vault kdf encode: %w", err)
		}
		installed, err := writeNewFileAtomic(kdfPath, blob, filesystem.ModePrivateFile)
		if err != nil {
			return nil, err
		}
		if installed {
			return key, nil
		}
		// Another process completed initialisation first. Always derive from the
		// KDF material it installed; returning our unpublished key would make
		// credentials written by this opener unrecoverable.
		winner, err := loadKDF(kdfPath)
		if err != nil {
			return nil, err
		}
		return deriveOrInit(pass, &winner, true, kdfPath)
	}
	derived := argon2.IDKey([]byte(pass), kdf.Salt, kdf.Time, kdf.Memory, kdf.Threads, kdf.KeyLength)
	aead, err := aeadFromKey(derived)
	if err != nil {
		return nil, err
	}
	if _, err := aead.Open(nil, kdf.VNonce, kdf.Verifier, nil); err != nil {
		return nil, ErrWrongPassphrase
	}
	return derived, nil
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

// loadKDF reads the KDF file or returns ErrNotFound.
func loadKDF(path string) (kdfFile, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return kdfFile{}, ErrNotFound
	}
	if err != nil {
		return kdfFile{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxEncodedHeader+1))
	if err != nil {
		return kdfFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxEncodedHeader {
		return kdfFile{}, fmt.Errorf("%w: file exceeds %d bytes", ErrInvalidKDF, maxEncodedHeader)
	}
	var f kdfFile
	if err := json.Unmarshal(data, &f); err != nil {
		return kdfFile{}, fmt.Errorf("%w: decode %s: %w", ErrInvalidKDF, path, err)
	}
	if err := validateKDF(f); err != nil {
		return kdfFile{}, fmt.Errorf("load %s: %w", path, err)
	}
	return f, nil
}

// loadLegacy builds an AEAD from legacy key material or returns ErrNotFound.
func loadLegacy(path string) (cipher.AEAD, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) != masterKeySize {
		return nil, fmt.Errorf("%s: %d bytes, want %d (legacy master key corrupt)", path, len(data), masterKeySize)
	}
	aead, err := aeadFromKey(data)
	if err != nil {
		return nil, err
	}
	return aead, nil
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

// writeNewFileAtomic installs a complete file only when path does not already
// exist. Hard-linking the synced temporary file is an atomic create operation:
// concurrent initialisers cannot overwrite one another's KDF material.
func writeNewFileAtomic(path string, data []byte, perm fs.FileMode) (bool, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vault-*")
	if err != nil {
		return false, fmt.Errorf("vault temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("vault chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("vault write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("vault sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("vault close: %w", err)
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("vault install: %w", err)
	}
	return true, nil
}

// Credential persistence (encrypt→store, fetch→decrypt) lives in
// prefs.Service, the single orchestrator that composes this vault with
// the db.Store. The vault stays focused on the crypto primitives
// (Encrypt / Decrypt) plus master-key management.
