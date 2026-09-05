package claude

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/prefs"
)

// CredentialStore is the persistence seam required by Claude OAuth flows.
// Applications may wrap the generic preference service without exposing it.
type CredentialStore interface {
	GetKeyEffective(context.Context, string) (prefs.KeyValue, error)
	SetKey(context.Context, prefs.Scope, string, string) error
}
