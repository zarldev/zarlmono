package backends

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var providerNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidateDefinition enforces insert-time rules for DB-backed providers.
func ValidateDefinition(def ProviderDefinition) error {
	if !providerNameRE.MatchString(def.Name) || strings.ContainsAny(def.Name, " \t\n") {
		return fmt.Errorf("%w: invalid name %q", ErrInvalidProviderDef, def.Name)
	}
	if !def.AdapterType.IsValid() {
		return fmt.Errorf("%w: invalid adapter type %q", ErrInvalidProviderDef, def.AdapterType.String())
	}
	requiresBaseURL := def.AdapterType == AdapterTypes.OPENAICOMPATIBLE ||
		def.AdapterType == AdapterTypes.DEEPSEEKCOMPATIBLE
	if requiresBaseURL && strings.TrimSpace(def.BaseURL) == "" {
		return fmt.Errorf("%w: base_url is required for %s", ErrInvalidProviderDef, def.AdapterType.String())
	}
	if def.BaseURL != "" {
		u, err := url.Parse(def.BaseURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("%w: invalid base_url %q", ErrInvalidProviderDef, def.BaseURL)
		}
		if u.User != nil {
			return fmt.Errorf("%w: base_url must not contain credentials", ErrInvalidProviderDef)
		}
	}
	if _, err := json.Marshal(def.SeedModels); err != nil {
		return fmt.Errorf("%w: seed_models: %w", ErrInvalidProviderDef, err)
	}
	return nil
}
