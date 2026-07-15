package dynamic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ProviderName is the registry-provider tag every dynamic tool is
// registered under. Lets callers list/clear all dynamic tools as a
// group.
const ProviderName = "dynamic"

// ErrNameExists is returned by Register when a tool of the same name
// is already registered under a different provider — refusing the
// registration is safer than silently shadowing a built-in.
var ErrNameExists = errors.New("dynamic: tool name already registered by another provider")

// ErrOutsideRoot is returned when Register is given a binary path that
// resolves outside the configured binary root. Only emitted when the
// registrar was constructed with WithBinaryRoot — an unconstrained
// registrar accepts any absolute path.
var ErrOutsideRoot = errors.New("dynamic: binary path outside configured root")

// Registrar wires a [Catalog] to a tools.Registry so that each
// catalog entry has a live BinaryTool registered. Add/Remove update
// both at once; Sync rebuilds the registry side from the catalog
// (used at startup after Catalog.Load).
type Registrar struct {
	catalog    *Catalog
	registry   *tools.Registry
	binaryRoot string // empty = unconstrained
	mu         sync.Mutex
}

// WithBinaryRoot constrains the Registrar to only accept binary paths
// inside the given directory. Pass the agent's workspace root so a
// stray register call can't point at /bin/sh. Symlinks are resolved
// before the boundary check so a symlink-to-outside is rejected.
func WithBinaryRoot(root string) options.Option[Registrar] {
	return func(r *Registrar) {
		if root == "" {
			return
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return
		}
		// EvalSymlinks fails on non-existing dirs; fall back to abs.
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		r.binaryRoot = abs
	}
}

// NewRegistrar wires a catalog to a tools.Registry.
func NewRegistrar(catalog *Catalog, registry *tools.Registry, opts ...options.Option[Registrar]) *Registrar {
	r := &Registrar{catalog: catalog, registry: registry}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Sync registers a BinaryTool for every catalog entry. Existing
// dynamic registrations are cleared first so the registry mirrors
// the catalog. BUILT-INS WIN over catalog entries that would shadow
// them: if a non-dynamic tool of the same name already lives in the
// registry (the runtime registered it before Sync ran, the common
// case for tools added to the binary in a later version), the
// catalog entry is skipped and a warning is logged. This catches
// the "I registered a dynamic web_search before the built-in
// shipped; now the built-in is being clobbered by my old binary
// every launch" trap — Register's collision check guards the
// /new_tool path, but Sync used to blindly overwrite on every
// restore.
//
// Returns the slice of shadowed names so the caller can surface
// them to the user (zarlcode appends them to its startup notices).
func (r *Registrar) Sync() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var shadowed []string
	r.registry.UnregisterProvider(ProviderName)
	for _, entry := range r.catalog.Entries() {
		// After UnregisterProvider above, anything still registered
		// under this name is a non-dynamic (built-in) tool. Don't
		// shadow it; surface the collision so the user can
		// /unregister the stale catalog entry if they want.
		if _, exists := r.registry.Tool(entry.Spec.Name); exists {
			shadowed = append(shadowed, string(entry.Spec.Name))
			slog.Warn("dynamic registrar: catalog entry shadows built-in; skipping",
				"name", entry.Spec.Name, "binary", entry.BinaryPath)
			continue
		}
		tool := NewBinaryTool(entry.Spec, entry.BinaryPath)
		if err := r.registry.RegisterWithProvider(tool, ProviderName); err != nil {
			slog.Warn("dynamic: skipping tool with invalid spec", "error", err)
			continue
		}
	}
	return shadowed, nil
}

// Register adds (or replaces) a dynamic tool: persists to the
// catalog and registers a fresh BinaryTool on the registry. Refuses
// to shadow a non-dynamic registration of the same name. If
// WithBinaryRoot was set, refuses paths that resolve outside that
// root.
func (r *Registrar) Register(spec tools.ToolSpec, binaryPath string) error {
	return r.RegisterContext(context.Background(), spec, binaryPath)
}

// RegisterContext adds (or replaces) a dynamic tool using the caller's context
// for catalog persistence.
func (r *Registrar) RegisterContext(ctx context.Context, spec tools.ToolSpec, binaryPath string) error {
	if spec.Name == "" {
		return errors.New("dynamic register: empty name")
	}
	if binaryPath == "" {
		return fmt.Errorf("dynamic register %q: empty binary path", spec.Name)
	}
	if err := r.checkBinaryRoot(binaryPath); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.registry.Tool(spec.Name); exists {
		if r.registry.ProviderFor(spec.Name) != ProviderName {
			return fmt.Errorf("%w: %s", ErrNameExists, spec.Name)
		}
	}

	if err := r.catalog.AddContext(ctx, Entry{Spec: spec, BinaryPath: binaryPath}); err != nil {
		return err
	}
	// Replace any prior dynamic registration of this name in-place.
	r.registry.Unregister(spec.Name)
	if err := r.registry.RegisterWithProvider(NewBinaryTool(spec, binaryPath), ProviderName); err != nil {
		return fmt.Errorf("dynamic register %q: %w", spec.Name, err)
	}
	return nil
}

// Unregister removes a dynamic tool from both the catalog and the
// registry. Treats the catalog as authoritative: only entries that
// were added via this registrar (i.e. live in the catalog) can be
// removed. Built-ins (bash, read, write, register_tool, ...) are
// part of the runtime; they're refused with a specific message.
func (r *Registrar) Unregister(name tools.ToolName) error {
	return r.UnregisterContext(context.Background(), name)
}

// UnregisterContext removes a dynamic tool using the caller's context for
// catalog persistence.
func (r *Registrar) UnregisterContext(ctx context.Context, name tools.ToolName) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed, err := r.catalog.RemoveContext(ctx, name)
	if err != nil {
		return err
	}
	if removed {
		r.registry.Unregister(name)
		return nil
	}
	// Not in catalog. If the name still resolves on the registry,
	// it's a built-in. Otherwise it never existed.
	if _, exists := r.registry.Tool(name); exists {
		return fmt.Errorf("%s is a built-in tool — not removable via unregister_tool", name)
	}
	return fmt.Errorf("no dynamic tool named %s (not in catalog, not in registry)", name)
}

// Catalog returns the underlying catalog (read access).
func (r *Registrar) Catalog() *Catalog { return r.catalog }

// BinaryRoot returns the configured binary root, or "" if unconstrained.
func (r *Registrar) BinaryRoot() string { return r.binaryRoot }

// checkBinaryRoot enforces the WithBinaryRoot constraint, if set. The
// path must be absolute, exist (its leaf may not), and resolve to a
// location inside the configured root after symlink evaluation.
func (r *Registrar) checkBinaryRoot(binaryPath string) error {
	if r.binaryRoot == "" {
		return nil
	}
	if !filepath.IsAbs(binaryPath) {
		return fmt.Errorf("%w: %q is not absolute", ErrOutsideRoot, binaryPath)
	}
	resolved := filepath.Clean(binaryPath)
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = eval
	}
	rel, err := filepath.Rel(r.binaryRoot, resolved)
	if err != nil {
		return fmt.Errorf("%w: %q (rel: %w)", ErrOutsideRoot, binaryPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %q (root: %q)", ErrOutsideRoot, binaryPath, r.binaryRoot)
	}
	return nil
}
