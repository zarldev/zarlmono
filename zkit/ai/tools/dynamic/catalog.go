package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// Entry is one catalog row: the spec the LLM sees plus the binary
// that backs it.
type Entry struct {
	Spec       tools.ToolSpec `json:"spec"`
	BinaryPath string         `json:"binary_path"`
}

// Store is the persistence boundary for dynamic-tool registrations.
// Implementations carry their own scope (a workspace path for the
// sqlite-backed zarlcode adapter, a file path for the file-backed
// FileStore tests use) so the Catalog layer doesn't need to plumb
// workspace strings through every call.
//
// Operations take a context so the sqlite adapter can honour query
// cancellation. The file-based path ignores ctx — fast enough that
// blocking is uninteresting.
type Store interface {
	List(ctx context.Context) ([]Entry, error)
	Upsert(ctx context.Context, e Entry) error
	Delete(ctx context.Context, name tools.ToolName) error
}

// Catalog is the in-memory cache + write-through over a [Store].
// It is the source of truth at runtime — Add/Remove persist
// immediately and Load reads back what's in the store.
//
// Catalog serializes each complete store and snapshot transition. Its mutex
// deliberately remains held during store I/O so concurrent operations cannot
// leave persistence and the in-memory snapshot representing different sets.
type Catalog struct {
	store Store

	mu      sync.Mutex
	entries []Entry
}

// NewCatalog wires a Catalog to a [Store]. The store carries its
// own scope (workspace path for sqlite, file path for the file-based
// fallback), so Catalog stays scope-agnostic.
func NewCatalog(store Store) *Catalog {
	return &Catalog{store: store}
}

// LoadContext reads the catalog from the store. A missing source is not
// an error — it produces an empty catalog.
func (c *Catalog) LoadContext(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	rows, err := c.store.List(ctx)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	c.entries = rows
	return nil
}

// Entries returns a snapshot of the current entries.
func (c *Catalog) Entries() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Get returns the entry for the given tool name, or false if absent.
func (c *Catalog) Get(name tools.ToolName) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.Spec.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// AddContext inserts (or replaces) an entry by name and persists to the
// store. The in-memory cache is updated only on a successful store write, so
// a transient store failure leaves the catalog unchanged.
func (c *Catalog) AddContext(ctx context.Context, entry Entry) error {
	if entry.Spec.Name == "" {
		return errors.New("catalog add: empty tool name")
	}
	if entry.BinaryPath == "" {
		return fmt.Errorf("catalog add %q: empty binary path", entry.Spec.Name)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.Upsert(ctx, entry); err != nil {
		return fmt.Errorf("catalog add %q: %w", entry.Spec.Name, err)
	}
	for i, current := range c.entries {
		if current.Spec.Name == entry.Spec.Name {
			c.entries[i] = entry
			return nil
		}
	}
	c.entries = append(c.entries, entry)
	return nil
}

// RemoveContext deletes the entry matching name. It is idempotent: absence is
// success. Persistence is completed before the snapshot changes.
func (c *Catalog) RemoveContext(ctx context.Context, name tools.ToolName) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	idx := -1
	for i, entry := range c.entries {
		if entry.Spec.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	if err := c.store.Delete(ctx, name); err != nil {
		return fmt.Errorf("catalog remove %q: %w", name, err)
	}
	c.entries = append(c.entries[:idx], c.entries[idx+1:]...)
	return nil
}

type fileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a [Store] backed by a JSON file at path. Used
// by tests (where in-memory store fakes would be heavier than just
// writing to t.TempDir()). Live workspaces use the sqlite-backed
// store via zarlcode/tui/dynamic_store.go instead.
//
// The file is created on the first Upsert if it does not exist;
// missing directories are created with filesystem.ModePublicDir.
func NewFileStore(path string) *fileStore {
	return &fileStore{path: path}
}

func (s *fileStore) List(ctx context.Context) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %q: %w", s.path, err)
	}
	return entries, nil
}

func (s *fileStore) Upsert(ctx context.Context, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readUnsafe()
	if err != nil {
		return err
	}
	replaced := false
	for i, e := range entries {
		if e.Spec.Name == entry.Spec.Name {
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}
	return s.writeUnsafe(entries)
}

func (s *fileStore) Delete(ctx context.Context, name tools.ToolName) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readUnsafe()
	if err != nil {
		return err
	}
	for i, e := range entries {
		if e.Spec.Name == name {
			entries = append(entries[:i], entries[i+1:]...)
			return s.writeUnsafe(entries)
		}
	}
	return nil
}

func (s *fileStore) readUnsafe() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %q: %w", s.path, err)
	}
	return entries, nil
}

func (s *fileStore) writeUnsafe(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), filesystem.ModePublicDir); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, filesystem.ModePublicFile); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
