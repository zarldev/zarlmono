package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// APIKeyStorage identifies how an api_keys row's material should be decoded.
type APIKeyStorage string

const (
	// APIKeyStorageVault means Ciphertext contains AES-GCM ciphertext and Nonce
	// contains its nonce. This is the pre-existing storage shape.
	APIKeyStorageVault APIKeyStorage = "vault"
	// APIKeyStoragePlaintext means Ciphertext contains the plaintext credential
	// bytes directly and Nonce is empty. The column keeps its historical name so
	// old migrations and generated code remain small; the Storage field is the
	// authority.
	APIKeyStoragePlaintext APIKeyStorage = "plaintext"
)

// APIKeyCiphertext is the raw stored material for one provider. For historical
// compatibility the credential bytes live in Ciphertext even when Storage is
// [APIKeyStoragePlaintext]; callers must branch on Storage before interpreting
// the bytes.
type APIKeyCiphertext struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
	Storage    APIKeyStorage
}

func normalizeAPIKeyStorage(storage APIKeyStorage) APIKeyStorage {
	switch storage {
	case APIKeyStoragePlaintext, APIKeyStorageVault:
		return storage
	default:
		return APIKeyStorageVault
	}
}

// GetAPIKey returns the stored ciphertext for (workspace, provider),
// with global fallback. Returns ([], false, nil) when no row exists.
func (s *Store) GetAPIKey(ctx context.Context, workspace, provider string) (APIKeyCiphertext, bool, error) {
	if v, ok, err := s.getAPIKeyRow(ctx, workspace, provider); err != nil || ok {
		return v, ok, err
	}
	if workspace == "" {
		return APIKeyCiphertext{}, false, nil
	}
	return s.getAPIKeyRow(ctx, "", provider)
}

// GetAPIKeyExact is GetAPIKey without the global fallback — the
// caller wants the row for this exact workspace, not the inherited
// default. Returns ([], false, nil) when no row exists at this
// workspace, regardless of whether a global row would shadow it.
//
// Mirrors GetSettingExact. Used by settingsService.GetKey when the
// caller asked for [scopeWorkspace] specifically — without this, a
// promote-then-read sequence would see the just-written global row
// echo back as if it were still the workspace row.
func (s *Store) GetAPIKeyExact(ctx context.Context, workspace, provider string) (APIKeyCiphertext, bool, error) {
	return s.getAPIKeyRow(ctx, workspace, provider)
}

func (s *Store) getAPIKeyRow(ctx context.Context, workspace, provider string) (APIKeyCiphertext, bool, error) {
	row, err := s.q.GetAPIKey(ctx, gen.GetAPIKeyParams{Workspace: workspace, Provider: provider})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIKeyCiphertext{}, false, nil
		}
		return APIKeyCiphertext{}, false, fmt.Errorf("get api key %q/%q: %w", workspace, provider, err)
	}
	return APIKeyCiphertext{
		Ciphertext: row.Ciphertext,
		Nonce:      row.Nonce,
		KeyVersion: int(row.KeyVersion),
		Storage:    normalizeAPIKeyStorage(APIKeyStorage(row.Storage)),
	}, true, nil
}

// SetAPIKey writes the ciphertext for (workspace, provider).
func (s *Store) SetAPIKey(ctx context.Context, workspace, provider string, ct APIKeyCiphertext) error {
	ct.Storage = normalizeAPIKeyStorage(ct.Storage)
	err := s.q.UpsertAPIKey(ctx, gen.UpsertAPIKeyParams{
		Workspace:  workspace,
		Provider:   provider,
		Ciphertext: ct.Ciphertext,
		Nonce:      ct.Nonce,
		KeyVersion: int64(ct.KeyVersion),
		Storage:    string(ct.Storage),
		UpdatedAt:  time.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("upsert api key %q/%q: %w", workspace, provider, err)
	}
	return nil
}

// APIKeyRecord is one full api_keys row including its scope. Returned by
// [Store.AllAPIKeys] for the vault key migration; ordinary reads use
// [Store.GetAPIKey] which never exposes the workspace/provider columns.
type APIKeyRecord struct {
	Workspace string
	Provider  string
	APIKeyCiphertext
}

// AllAPIKeys returns every stored credential across all workspaces, with
// ciphertext. Used only by the one-time vault key migration to re-encrypt
// rows under a new master key.
func (s *Store) AllAPIKeys(ctx context.Context) ([]APIKeyRecord, error) {
	rows, err := s.q.ListAllAPIKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all api keys: %w", err)
	}
	out := make([]APIKeyRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, APIKeyRecord{
			Workspace: r.Workspace,
			Provider:  r.Provider,
			APIKeyCiphertext: APIKeyCiphertext{
				Ciphertext: r.Ciphertext,
				Nonce:      r.Nonce,
				KeyVersion: int(r.KeyVersion),
				Storage:    normalizeAPIKeyStorage(APIKeyStorage(r.Storage)),
			},
		})
	}
	return out, nil
}

// DeleteAPIKey removes a stored ciphertext.
func (s *Store) DeleteAPIKey(ctx context.Context, workspace, provider string) error {
	err := s.q.DeleteAPIKey(ctx, gen.DeleteAPIKeyParams{Workspace: workspace, Provider: provider})
	if err != nil {
		return fmt.Errorf("delete api key %q/%q: %w", workspace, provider, err)
	}
	return nil
}

// ListAPIKeyProviders returns the union of provider names available
// to workspace (workspace-specific + global, deduped). Order is
// alphabetical. The actual key material is never returned.
func (s *Store) ListAPIKeyProviders(ctx context.Context, workspace string) ([]string, error) {
	globals, err := s.q.ListAPIKeyProvidersByWorkspace(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list global api-key providers: %w", err)
	}
	seen := make(map[string]struct{}, len(globals))
	for _, p := range globals {
		seen[p] = struct{}{}
	}
	if workspace != "" {
		local, err := s.q.ListAPIKeyProvidersByWorkspace(ctx, workspace)
		if err != nil {
			return nil, fmt.Errorf("list workspace api-key providers: %w", err)
		}
		for _, p := range local {
			seen[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	// Deterministic order — callers that show a provider list rely
	// on stable ordering across restarts.
	slices.Sort(out)
	return out, nil
}

// toSessionRecord maps a generated row to the domain transport type.
func toSessionRecord(r gen.Session) SessionRecord {
	return SessionRecord{
		ID:             r.ID,
		Workspace:      r.Workspace,
		Label:          r.Label,
		AgentName:      r.AgentName,
		Provider:       r.Provider,
		Model:          r.Model,
		HistoryJSON:    []byte(r.HistoryJson),
		PendingJSON:    []byte(r.PendingJson),
		LastUsageJSON:  []byte(r.LastUsageJson),
		DiffBodiesJSON: []byte(r.DiffBodiesJson),
		PlanJSON:       []byte(r.PlanJson),
		CreatedAt:      time.Unix(r.CreatedAt, 0),
		UpdatedAt:      time.Unix(r.UpdatedAt, 0),
	}
}

// orEmpty returns b when non-empty, otherwise the JSON-safe fallback.
// Sqlite NOT NULL columns can't hold nil; the schema defaults already
// emit '[]' / '{}' / 'null', but the upsert path bypasses defaults
// so we mirror them here.
func orEmpty(b []byte, fallback string) []byte {
	if len(b) == 0 {
		return []byte(fallback)
	}
	return b
}
