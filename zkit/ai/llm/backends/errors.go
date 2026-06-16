package backends

import "errors"

// Sentinel errors returned by the ProviderRegistry and adapter layer.
// Consumers match against these to distinguish user-facing messages
// from internal surprises.

var (
	// ErrProviderNotFound is returned when a provider name doesn't exist
	// in the registry (neither built-in nor DB).
	ErrProviderNotFound = errors.New("provider registry: provider not found")

	// ErrProviderDisabled is returned when the provider exists but its
	// enabled flag is false.
	ErrProviderDisabled = errors.New("provider registry: provider disabled")

	// ErrProviderBuiltin is returned when a caller attempts to modify or
	// delete a built-in provider.
	ErrProviderBuiltin = errors.New("provider registry: cannot modify built-in provider")

	// ErrProviderActive is returned when a caller attempts to delete
	// the currently-active provider. Switch away first.
	ErrProviderActive = errors.New("provider registry: cannot modify active provider")

	// ErrInvalidProviderDef is returned when a provider definition fails
	// insertion-time validation (bad name, URL, adapter type, etc.).
	ErrInvalidProviderDef = errors.New("provider registry: invalid provider definition")

	// ErrRegistryInternal is returned for type-assertion failures and
	// other internal invariants.
	ErrRegistryInternal = errors.New("provider registry: internal error")

	// ErrNoStore is returned by mutating operations (UpsertProvider,
	// Delete) when the registry was constructed without a Store — a
	// built-ins-only registry can't persist custom providers.
	ErrNoStore = errors.New("provider registry: no store configured")
)
