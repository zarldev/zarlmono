package prefs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// Service is the single funnel every persisted preference
// flows through — both the plaintext settings table AND the encrypted
// api_keys vault. It owns the store↔vault composition (encrypt→store on
// write, store→decrypt on read); the vault stays a pure crypto
// primitive and the store a pure key/value sink, so the precedence +
// scope rules live in one place.
//
// Why an explicit [scope] enum instead of "workspace string":
//
//	The previous shape used the empty string to mean "global" (matching
//	the underlying schema), which let one off-by-one bug — passing
//	m.wsRoot when "" was meant — silently write to the wrong scope.
//	Workspace-vs-global is a user-visible distinction that should be a
//	first-class type, not a sentinel.
//
// Why one service for two underlying tables (settings + api_keys):
//
//	Both share the same precedence chain (workspace then global), the
//	same Promote/Demote semantics, and the same UX surface (settings
//	pane rows look identical to the user). Splitting the API by table
//	would force every call site to know which one it's talking to;
//	consolidating means the pane / CLI / wizard / OAuth code can stay
//	scope-aware without re-implementing the lookup chain.
//
//	The methods stay split (GetSetting vs GetKey) because the value
//	shapes differ — strings vs encrypted plaintext — and pretending
//	they're the same would require an awkward `any` return.
type Service struct {
	store  *db.Store
	mu     sync.RWMutex // protects vault replacement and credential transitions
	vault  *vault.Vault // nil when keys subsystem isn't initialised yet
	wsRoot string       // resolved workspace root; never empty in normal startup
}

// NewService constructs the service. wsRoot must be non-empty
// — every interactive shell has a workspace at this point (main.go's
// fallback to a temp dir guarantees it). CLI subcommands that have no
// workspace context pass "" explicitly and only call the global-scope
// methods; the workspace-scope methods return ErrNoWorkspace there.
func NewService(store *db.Store, v *vault.Vault, wsRoot string) *Service {
	return &Service{store: store, vault: v, wsRoot: wsRoot}
}

// SettingChange is one operation in an atomic preference update. Delete removes
// Key; otherwise Value must be non-empty and is persisted exactly.
type SettingChange struct {
	Key    string
	Value  string
	Delete bool
}

// Stable scope names retained for callers; generated values own validity,
// parsing, serialization, and exhaustive iteration.
var (
	ScopeWorkspace = Scopes.WORKSPACE
	ScopeGlobal    = Scopes.GLOBAL
	ScopeEffective = Scopes.EFFECTIVE
)

// ErrNoWorkspace is returned when a workspace-scope operation runs in
// a context that has no workspace. Today only the CLI subcommand path
// hits this — interactive shells always have a wsRoot.
var ErrNoWorkspace = errors.New("settings: no workspace in this context")

// ErrInvalidScope is returned when a writer is called with
// [ScopeEffective]. Writes are explicit; the caller must pick a real
// row to land in.
var ErrInvalidScope = errors.New("settings: ScopeEffective is read-only")

// ErrNoVault is returned by the key operations when no vault is
// available (the vault.Open path failed at startup). Surfaced as an
// actionable message; the caller fixes by re-running with a working
// XDG state dir.
var ErrNoVault = errors.New("settings: vault not initialised")

// ErrCredentialsLocked is returned when a stored credential row is encrypted
// but no vault is currently unlocked. Plaintext rows remain readable without a
// vault; callers see this only for passphrase-protected material.
var ErrCredentialsLocked = errors.New("settings: encrypted credentials are locked")

// ErrNotFound is returned when no preference exists at the requested scope.
var ErrNotFound = errors.New("settings: not found")

const (
	// CredentialProtectionOff stores credential rows as plaintext in state.db.
	CredentialProtectionOff = "off"
	// CredentialProtectionPassphrase stores credential rows encrypted via vault.
	CredentialProtectionPassphrase = "passphrase"
)

// SettingValue is a settings-table read result: the value plus the
// scope it resolved from (workspace / global / "" when missing).
// Source is "" when ok is false.
type SettingValue struct {
	Value  string
	Source Scope
}

// KeyValue is the api_keys-table counterpart to [SettingValue]: a
// decrypted credential plus the scope it resolved from. Returned by
// [Service.GetKeyEffective] so callers that need to write
// back to the same row (notably the OAuth token-refresh path) can
// pick the right scope without re-implementing the precedence chain.
type KeyValue struct {
	Value  string
	Source Scope
}

// HasVault reports whether the service was constructed with a usable
// vault. Callers that need to know whether key operations would
// succeed before attempting one (e.g. provider builders gating on
// "do we have OAuth support") check this rather than catching
// [ErrNoVault] after the fact.
func (s *Service) HasVault() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vault != nil
}

// CredentialProtection reports the database-wide credential storage mode. An
// explicit global setting wins. Workspace settings never affect key storage.
// Without one, any existing vault-backed row preserves passphrase mode for
// upgraded installs; otherwise fresh/plaintext installs default to off.
func (s *Service) CredentialProtection(ctx context.Context) (string, error) {
	sv, err := s.GetSetting(ctx, ScopeGlobal, credentialProtectionSetting)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if err == nil {
		switch sv.Value {
		case CredentialProtectionPassphrase:
			return CredentialProtectionPassphrase, nil
		case CredentialProtectionOff:
			return CredentialProtectionOff, nil
		}
	}
	rows, err := s.store.AllAPIKeys(ctx)
	if err != nil {
		return "", fmt.Errorf("settings: inspect credential rows: %w", err)
	}
	for _, r := range rows {
		if r.Storage == db.APIKeyStorageVault {
			return CredentialProtectionPassphrase, nil
		}
	}
	return CredentialProtectionOff, nil
}

// HasVaultBackedKeys reports whether any credential row still requires a vault
// to decode. Startup uses this to decide whether an unlock prompt is necessary.
func (s *Service) HasVaultBackedKeys(ctx context.Context) (bool, error) {
	rows, err := s.store.AllAPIKeys(ctx)
	if err != nil {
		return false, fmt.Errorf("settings: inspect credential rows: %w", err)
	}
	for _, r := range rows {
		if r.Storage == db.APIKeyStorageVault {
			return true, nil
		}
	}
	return false, nil
}

// SetVault replaces the currently unlocked vault. It is used by startup and
// protection toggles after opening/creating the vault outside NewService.
func (s *Service) SetVault(v *vault.Vault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vault = v
}

// GetSetting reads a setting and reports its resolved scope.
func (s *Service) GetSetting(ctx context.Context, sc Scope, key string) (SettingValue, error) {
	read := func(workspace string, source Scope) (SettingValue, error) {
		v, err := s.store.GetSettingExact(ctx, workspace, key)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return SettingValue{}, ErrNotFound
			}
			return SettingValue{}, err
		}
		return SettingValue{Value: v, Source: source}, nil
	}
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return SettingValue{}, ErrNoWorkspace
		}
		return read(s.wsRoot, ScopeWorkspace)
	case ScopeGlobal:
		return read("", ScopeGlobal)
	case ScopeEffective:
		if s.wsRoot != "" {
			v, err := read(s.wsRoot, ScopeWorkspace)
			if err == nil && v.Value != "" {
				return v, nil
			}
			if err != nil && !errors.Is(err, ErrNotFound) {
				return SettingValue{}, err
			}
		}
		return read("", ScopeGlobal)
	default:
		return SettingValue{}, fmt.Errorf("settings: unknown scope %d", sc)
	}
}

// SetSetting writes a setting at the explicit scope. Empty values are
// rejected — callers wanting to clear a row use DeleteSetting so the
// intent is unambiguous in the log.
func (s *Service) SetSetting(ctx context.Context, sc Scope, key, value string) error {
	if value == "" {
		return errors.New("settings: SetSetting with empty value; use DeleteSetting")
	}
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return ErrNoWorkspace
		}
		return s.store.SetSetting(ctx, s.wsRoot, key, value)
	case ScopeGlobal:
		return s.store.SetSetting(ctx, "", key, value)
	case ScopeEffective:
		return ErrInvalidScope
	}
	return fmt.Errorf("settings: unknown scope %d", sc)
}

// ApplySettings atomically applies changes at an explicit scope. It is the
// generic transaction boundary for application-owned groups of preferences.
func (s *Service) ApplySettings(ctx context.Context, sc Scope, changes ...SettingChange) error {
	workspace, err := s.writeWorkspace(sc)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if change.Key == "" {
			return errors.New("settings: change requires key")
		}
		if !change.Delete && change.Value == "" {
			return errors.New("settings: change with empty value must be a delete")
		}
	}
	return s.store.WithTx(ctx, func(tx *db.Store) error {
		for _, change := range changes {
			if change.Delete {
				if err := tx.DeleteSetting(ctx, workspace, change.Key); err != nil {
					return fmt.Errorf("delete setting %q: %w", change.Key, err)
				}
				continue
			}
			if err := tx.SetSetting(ctx, workspace, change.Key, change.Value); err != nil {
				return fmt.Errorf("set setting %q: %w", change.Key, err)
			}
		}
		return nil
	})
}

func (s *Service) writeWorkspace(sc Scope) (string, error) {
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return "", ErrNoWorkspace
		}
		return s.wsRoot, nil
	case ScopeGlobal:
		return "", nil
	case ScopeEffective:
		return "", ErrInvalidScope
	default:
		return "", fmt.Errorf("settings: unknown scope %d", sc)
	}
}

// DeleteSetting removes a setting at the explicit scope. Returns nil
// on missing rows — delete is idempotent.
func (s *Service) DeleteSetting(ctx context.Context, sc Scope, key string) error {
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return ErrNoWorkspace
		}
		return s.store.DeleteSetting(ctx, s.wsRoot, key)
	case ScopeGlobal:
		return s.store.DeleteSetting(ctx, "", key)
	case ScopeEffective:
		return ErrInvalidScope
	}
	return fmt.Errorf("settings: unknown scope %d", sc)
}

// PromoteSetting moves the workspace row's value into the global row,
// then deletes the workspace row. Semantics: "stop being a per-
// workspace pin, become the default every workspace inherits."
//
// MOVE rather than COPY because copy creates silent drift — a later
// edit on the workspace row would diverge from global without
// signalling it. Move forces re-promote when the user wants to
// re-publish a change.
func (s *Service) PromoteSetting(ctx context.Context, key string) error {
	if s.wsRoot == "" {
		return ErrNoWorkspace
	}
	// One transaction so a crash between the global write and the workspace
	// delete can't leave the value shadowed in BOTH scopes (it's a MOVE).
	return s.store.WithTx(ctx, func(tx *db.Store) error {
		v, err := tx.GetSettingExact(ctx, s.wsRoot, key)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return fmt.Errorf("promote setting %q: no workspace row to promote: %w", key, ErrNotFound)
			}
			return fmt.Errorf("promote setting %q: read workspace: %w", key, err)
		}
		if v == "" {
			return fmt.Errorf("promote setting %q: no workspace row to promote: %w", key, ErrNotFound)
		}
		if err := tx.SetSetting(ctx, "", key, v); err != nil {
			return fmt.Errorf("promote setting %q: write global: %w", key, err)
		}
		if err := tx.DeleteSetting(ctx, s.wsRoot, key); err != nil {
			return fmt.Errorf("promote setting %q: drop workspace: %w", key, err)
		}
		return nil
	})
}

// GetKey reads an API key at the requested scope.
func (s *Service) GetKey(ctx context.Context, sc Scope, provider string) (string, error) {
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return "", ErrNoWorkspace
		}
		return s.exactKey(ctx, s.wsRoot, provider)
	case ScopeGlobal:
		return s.exactKey(ctx, "", provider)
	case ScopeEffective:
		if s.wsRoot != "" {
			k, err := s.exactKey(ctx, s.wsRoot, provider)
			if err == nil && k != "" {
				return k, nil
			}
			if err != nil && !errors.Is(err, ErrNotFound) {
				return "", err
			}
		}
		return s.exactKey(ctx, "", provider)
	default:
		return "", fmt.Errorf("settings: unknown scope %d", sc)
	}
}

// GetKeyEffective reads an API key and reports the scope it resolved from.
func (s *Service) GetKeyEffective(ctx context.Context, provider string) (KeyValue, error) {
	if s.wsRoot != "" {
		k, err := s.exactKey(ctx, s.wsRoot, provider)
		if err == nil && k != "" {
			return KeyValue{Value: k, Source: ScopeWorkspace}, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return KeyValue{}, err
		}
	}
	k, err := s.exactKey(ctx, "", provider)
	if err != nil {
		return KeyValue{}, err
	}
	return KeyValue{Value: k, Source: ScopeGlobal}, nil
}

func (s *Service) exactKey(ctx context.Context, workspace, provider string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ct, err := s.store.GetAPIKeyExact(ctx, workspace, provider)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	if ct.Storage == db.APIKeyStoragePlaintext {
		return string(ct.Ciphertext), nil
	}
	if s.vault == nil {
		return "", ErrCredentialsLocked
	}
	plain, err := s.vault.Decrypt(ct.Ciphertext, ct.Nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt api key for %q: %w", provider, err)
	}
	return plain, nil
}

// SetKey writes a plaintext api-key at the explicit scope. The vault
// encrypts before persisting. Empty plaintext is rejected — callers
// use DeleteKey to clear.
func (s *Service) SetKey(ctx context.Context, sc Scope, provider, plaintext string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plaintext == "" {
		return errors.New("settings: SetKey with empty plaintext; use DeleteKey")
	}
	var workspace string
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return ErrNoWorkspace
		}
		workspace = s.wsRoot
	case ScopeGlobal:
		workspace = ""
	case ScopeEffective:
		return ErrInvalidScope
	default:
		return fmt.Errorf("settings: unknown scope %d", sc)
	}
	mode, err := s.CredentialProtection(ctx)
	if err != nil {
		return err
	}
	return s.writeKey(ctx, workspace, provider, plaintext, mode)
}

// writeKey persists plaintext at (workspace, provider), encrypting only when
// the current credential-protection mode requires passphrase storage.
func (s *Service) writeKey(ctx context.Context, workspace, provider, plaintext, mode string) error {
	return s.writeKeyToStore(s.store, ctx, workspace, provider, plaintext, mode)
}

func (s *Service) writeKeyToStore(store *db.Store, ctx context.Context, workspace, provider, plaintext, mode string) error {
	if mode != CredentialProtectionPassphrase {
		return store.SetAPIKey(ctx, workspace, provider, db.APIKeyCiphertext{
			Ciphertext: []byte(plaintext),
			Nonce:      nil,
			KeyVersion: 0,
			Storage:    db.APIKeyStoragePlaintext,
		})
	}
	if s.vault == nil {
		return ErrCredentialsLocked
	}
	ct, nonce, err := s.vault.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt api key for %q: %w", provider, err)
	}
	return store.SetAPIKey(ctx, workspace, provider, db.APIKeyCiphertext{
		Ciphertext: ct,
		Nonce:      nonce,
		KeyVersion: vault.CurrentKeyVersion,
		Storage:    db.APIKeyStorageVault,
	})
}

// MigrateVaultKeys re-encrypts every stored credential under the vault's
// current (primary) key when a legacy master.key is still present, then
// removes the legacy key. It's the automatic, no-rekey-command migration from
// the old random-key scheme to the passphrase-derived one.
//
// Each row is decrypted (the vault transparently tries the legacy key) and
// re-encrypted under the primary key. The legacy key is removed only when
// EVERY row migrated cleanly — a partial failure leaves master.key in place so
// the still-old rows remain decryptable (both keys stay available). Returns the
// number of rows re-encrypted. A no-op (returns 0, nil) when there's no vault
// or no legacy key.
func (s *Service) MigrateVaultKeys(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vault == nil || !s.vault.HasLegacy() {
		return 0, nil
	}
	migrated := 0
	if err := s.store.WithTx(ctx, func(tx *db.Store) error {
		rows, err := tx.AllAPIKeys(ctx)
		if err != nil {
			return fmt.Errorf("list keys for migration: %w", err)
		}
		for _, r := range rows {
			if r.Storage == db.APIKeyStoragePlaintext {
				continue
			}
			plain, err := s.vault.Decrypt(r.Ciphertext, r.Nonce)
			if err != nil {
				return fmt.Errorf("decrypt key %q: %w", r.Provider, err)
			}
			if err := s.writeKeyToStore(tx, ctx, r.Workspace, r.Provider, plain, CredentialProtectionPassphrase); err != nil {
				return err
			}
			migrated++
		}
		return nil
	}); err != nil {
		return migrated, fmt.Errorf("settings: migrate legacy vault keys: %w", err)
	}
	if err := s.vault.RemoveLegacy(); err != nil {
		return migrated, fmt.Errorf("settings: remove legacy master key: %w", err)
	}
	return migrated, nil
}

// EnableCredentialProtection encrypts every plaintext credential row and marks
// future writes as passphrase-protected. Existing encrypted rows are preserved.
// The caller must supply an unlocked vault through NewService or SetVault first.
func (s *Service) EnableCredentialProtection(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vault == nil {
		return 0, ErrNoVault
	}
	rows, err := s.store.AllAPIKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("settings: list keys for protection enable: %w", err)
	}
	migrated := 0
	if err := s.store.WithTx(ctx, func(tx *db.Store) error {
		for _, r := range rows {
			if r.Storage == db.APIKeyStorageVault {
				continue
			}
			ct, nonce, err := s.vault.Encrypt(string(r.Ciphertext))
			if err != nil {
				return fmt.Errorf("encrypt key %q: %w", r.Provider, err)
			}
			if err := tx.SetAPIKey(ctx, r.Workspace, r.Provider, db.APIKeyCiphertext{
				Ciphertext: ct,
				Nonce:      nonce,
				KeyVersion: vault.CurrentKeyVersion,
				Storage:    db.APIKeyStorageVault,
			}); err != nil {
				return err
			}
			migrated++
		}
		if err := tx.SetSetting(ctx, "", credentialProtectionSetting, CredentialProtectionPassphrase); err != nil {
			return fmt.Errorf("set credential protection: %w", err)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return migrated, nil
}

// DisableCredentialProtection decrypts every encrypted credential row and marks
// future writes as plaintext. Encrypted rows require a vault already unlocked
// by the caller; the service never prompts or chooses a vault location. The
// rewrite and setting change are one transaction so a failure leaves the mode intact.
func (s *Service) DisableCredentialProtection(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hasVaultRows, err := s.HasVaultBackedKeys(ctx)
	if err != nil {
		return 0, err
	}
	if hasVaultRows && s.vault == nil {
		return 0, ErrCredentialsLocked
	}
	rows, err := s.store.AllAPIKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("settings: list keys for protection disable: %w", err)
	}
	migrated := 0
	if err := s.store.WithTx(ctx, func(tx *db.Store) error {
		for _, r := range rows {
			if r.Storage == db.APIKeyStoragePlaintext {
				continue
			}
			if s.vault == nil {
				return ErrCredentialsLocked
			}
			plain, err := s.vault.Decrypt(r.Ciphertext, r.Nonce)
			if err != nil {
				return fmt.Errorf("decrypt key %q: %w", r.Provider, err)
			}
			if err := tx.SetAPIKey(ctx, r.Workspace, r.Provider, db.APIKeyCiphertext{
				Ciphertext: []byte(plain),
				Nonce:      nil,
				KeyVersion: 0,
				Storage:    db.APIKeyStoragePlaintext,
			}); err != nil {
				return err
			}
			migrated++
		}
		if err := tx.SetSetting(ctx, "", credentialProtectionSetting, CredentialProtectionOff); err != nil {
			return fmt.Errorf("set credential protection: %w", err)
		}
		_ = tx.DeleteSetting(ctx, "", legacyVaultPromptSetting)
		if s.wsRoot != "" {
			_ = tx.DeleteSetting(ctx, s.wsRoot, legacyVaultPromptSetting)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return migrated, nil
}

// MigrateCredentialProtection consumes a global legacy vault_prompt=off marker
// by applying the current plaintext mode transition and deleting the marker.
// Workspace preferences cannot change the database-wide credential policy. It
// is idempotent and returns zero when no global migration is pending.
func (s *Service) MigrateCredentialProtection(ctx context.Context) (int, error) {
	sv, err := s.GetSetting(ctx, ScopeGlobal, legacyVaultPromptSetting)
	if errors.Is(err, ErrNotFound) || (err == nil && sv.Value != CredentialProtectionOff) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read legacy credential protection: %w", err)
	}
	return s.DisableCredentialProtection(ctx)
}

// DeleteKey removes an api-key at the explicit scope. Returns nil on
// missing rows — delete is idempotent.
func (s *Service) DeleteKey(ctx context.Context, sc Scope, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return ErrNoWorkspace
		}
		return s.store.DeleteAPIKey(ctx, s.wsRoot, provider)
	case ScopeGlobal:
		return s.store.DeleteAPIKey(ctx, "", provider)
	case ScopeEffective:
		return ErrInvalidScope
	}
	return fmt.Errorf("settings: unknown scope %d", sc)
}

// ListKeys returns the provider names with a stored api-key row at
// the requested scope. ScopeEffective returns the union (workspace
// shadows global by provider name; the list itself contains both
// sets so callers see every provider with any key set).
func (s *Service) ListKeys(ctx context.Context, sc Scope) ([]string, error) {
	switch sc {
	case ScopeWorkspace:
		if s.wsRoot == "" {
			return nil, ErrNoWorkspace
		}
		return s.store.ListAPIKeyProviders(ctx, s.wsRoot)
	case ScopeGlobal:
		return s.store.ListAPIKeyProviders(ctx, "")
	case ScopeEffective:
		seen := map[string]struct{}{}
		if s.wsRoot != "" {
			ws, err := s.store.ListAPIKeyProviders(ctx, s.wsRoot)
			if err != nil {
				return nil, err
			}
			for _, p := range ws {
				seen[p] = struct{}{}
			}
		}
		gl, err := s.store.ListAPIKeyProviders(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, p := range gl {
			seen[p] = struct{}{}
		}
		out := make([]string, 0, len(seen))
		for p := range seen {
			out = append(out, p)
		}
		return out, nil
	}
	return nil, fmt.Errorf("settings: unknown scope %d", sc)
}

// PromoteKey moves the workspace api-key row to the global row, then
// deletes the workspace row. See PromoteSetting for the rationale on
// MOVE vs COPY semantics.
//
// Uses exactKey on the read side so a missing workspace row isn't
// masked by a pre-existing global one (which would have promoted the
// global value into itself — a no-op that looks like success).
func (s *Service) PromoteKey(ctx context.Context, provider string) error {
	if s.wsRoot == "" {
		return ErrNoWorkspace
	}
	// One transaction so the global write and workspace delete are atomic (a
	// MOVE, never a half-applied copy). The decrypt/re-encrypt runs inside it
	// too — cheap, and keeps the whole promote on one consistent view.
	return s.store.WithTx(ctx, func(tx *db.Store) error {
		ct, err := tx.GetAPIKeyExact(ctx, s.wsRoot, provider)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return fmt.Errorf("promote key %q: no workspace row to promote: %w", provider, ErrNotFound)
			}
			return fmt.Errorf("promote key %q: read workspace: %w", provider, err)
		}
		if ct.Storage == db.APIKeyStorageVault && s.vault == nil {
			return ErrCredentialsLocked
		}
		if err := tx.SetAPIKey(ctx, "", provider, db.APIKeyCiphertext{
			Ciphertext: ct.Ciphertext,
			Nonce:      ct.Nonce,
			KeyVersion: ct.KeyVersion,
			Storage:    ct.Storage,
		}); err != nil {
			return fmt.Errorf("promote key %q: write global: %w", provider, err)
		}
		if err := tx.DeleteAPIKey(ctx, s.wsRoot, provider); err != nil {
			return fmt.Errorf("promote key %q: drop workspace: %w", provider, err)
		}
		return nil
	})
}
