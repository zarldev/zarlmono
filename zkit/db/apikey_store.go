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

// Stable storage names retained for callers; generated values own validity,
// parsing, serialization, and exhaustive iteration.
var (
	APIKeyStorageVault     = APIKeyStorages.VAULT
	APIKeyStoragePlaintext = APIKeyStorages.PLAINTEXT
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
	if storage.IsValid() {
		return storage
	}
	return APIKeyStorageVault
}

func parseAPIKeyStorageOrLegacy(raw string) APIKeyStorage {
	storage, err := ParseAPIKeyStorage(raw)
	if err != nil {
		return APIKeyStorageVault
	}
	return storage
}

// GetAPIKey reads stored key material with global fallback.
func (s *Store) GetAPIKey(ctx context.Context, workspace, provider string) (APIKeyCiphertext, error) {
	v, err := s.getAPIKeyRow(ctx, workspace, provider)
	if err == nil || !errors.Is(err, ErrNotFound) || workspace == "" {
		return v, err
	}
	return s.getAPIKeyRow(ctx, "", provider)
}

// GetAPIKeyExact reads the exact workspace row without global fallback.
func (s *Store) GetAPIKeyExact(ctx context.Context, workspace, provider string) (APIKeyCiphertext, error) {
	return s.getAPIKeyRow(ctx, workspace, provider)
}

func (s *Store) getAPIKeyRow(ctx context.Context, workspace, provider string) (APIKeyCiphertext, error) {
	row, err := s.q.GetAPIKey(ctx, gen.GetAPIKeyParams{Workspace: workspace, Provider: provider})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIKeyCiphertext{}, ErrNotFound
		}
		return APIKeyCiphertext{}, fmt.Errorf("get api key %q/%q: %w", workspace, provider, err)
	}
	storage := parseAPIKeyStorageOrLegacy(row.Storage)
	return APIKeyCiphertext{
		Ciphertext: append([]byte(nil), row.Ciphertext...),
		Nonce:      append([]byte(nil), row.Nonce...),
		KeyVersion: int(row.KeyVersion),
		Storage:    storage,
	}, nil
}

// SetAPIKey writes the ciphertext for (workspace, provider).
func (s *Store) SetAPIKey(ctx context.Context, workspace, provider string, ct APIKeyCiphertext) error {
	ct.Storage = normalizeAPIKeyStorage(ct.Storage)
	ct.Ciphertext = append([]byte(nil), ct.Ciphertext...)
	ct.Nonce = append([]byte(nil), ct.Nonce...)
	err := s.q.UpsertAPIKey(ctx, gen.UpsertAPIKeyParams{
		Workspace:  workspace,
		Provider:   provider,
		Ciphertext: ct.Ciphertext,
		Nonce:      ct.Nonce,
		KeyVersion: int64(ct.KeyVersion),
		Storage:    ct.Storage.String(),
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
				Storage:    parseAPIKeyStorageOrLegacy(r.Storage),
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

func sessionSummaryRowToRecord(r gen.ListSessionSummariesByWorkspaceRow) SessionRecord {
	return SessionRecord{
		ID:           r.ID,
		Label:        r.Label,
		Provider:     r.Provider,
		Model:        r.Model,
		MessageCount: int(r.MessageCount),
		CreatedAt:    time.Unix(r.CreatedAt, 0),
		UpdatedAt:    time.Unix(r.UpdatedAt, 0),
	}
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
		ToolTraceJSON:  []byte(r.ToolTraceJson),
		MessageCount:   int(r.MessageCount),
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
