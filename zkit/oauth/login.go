package oauth

import (
	"context"
	"fmt"
	"io"

	"github.com/zarldev/zarlmono/zkit/oauth/claude"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// RunLogin dispatches an interactive login to the named provider's flow:
// [codex.RunLogin] for codex.CredProvider ("openai-codex"),
// [claude.RunLogin] for claude.CredProvider ("claude-code"). It is the
// single CLI entry point (`zarlcode keys oauth <provider>`); front-ends
// that drive a flow themselves call into the provider subpackage
// directly.
func RunLogin(
	ctx context.Context,
	svc *prefs.Service,
	provider string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	switch provider {
	case claude.CredProvider:
		return claude.RunLogin(ctx, svc, stdin, stdout)
	case codex.CredProvider:
		return codex.RunLogin(ctx, svc, stdin, stdout)
	default:
		return fmt.Errorf(
			"oauth: provider %q is not supported (supported: %q, %q)",
			provider,
			codex.CredProvider,
			claude.CredProvider,
		)
	}
}
