package cli

// Sudo askpass client.
//
// The bash tool spawns children with Setsid (no controlling TTY) so
// programs like sudo can't read passwords directly from /dev/tty.
// To still let `sudo -A <cmd>` work, the interactive shell provides an
// askpass helper that talks back to the shell process — which DOES have
// the TTY — over a unix socket.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
)

// AskpassCommand executes the askpass socket protocol. Sock may be supplied by
// an embedder; its zero value reads the production socket environment variable.
type AskpassCommand struct {
	Sock string
}

// Execute sends the requested prompt to the owning TUI and writes the returned
// password to stdout using sudo's one-line askpass protocol.
func (c AskpassCommand) Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	sock := c.Sock
	if sock == "" {
		sock = os.Getenv(askpass.EnvSock)
	}
	if sock == "" {
		fmt.Fprintln(stderr, "zarlcode-askpass: ZARLCODE_ASKPASS_SOCK is unset")
		return 2
	}
	prompt := "Password:"
	if len(args) > 0 {
		prompt = strings.TrimSpace(strings.Join(args, " "))
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
	if err != nil {
		fmt.Fprintln(stderr, "zarlcode-askpass: dial:", err)
		return 2
	}
	defer conn.Close()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()

	if err := json.NewEncoder(conn).Encode(askpass.Request{Prompt: prompt}); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		fmt.Fprintln(stderr, "zarlcode-askpass: send:", err)
		return 2
	}
	var resp askpass.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		fmt.Fprintln(stderr, "zarlcode-askpass: recv:", err)
		return 2
	}
	if resp.Error != "" {
		fmt.Fprintln(stderr, "zarlcode-askpass:", resp.Error)
		return 2
	}
	fmt.Fprintln(stdout, resp.Password)
	return 0
}

// RunAskpassClient is the entry point for `zarlcode --askpass`.
func RunAskpassClient(args []string) {
	os.Exit((AskpassCommand{}).Execute(context.Background(), args, os.Stdout, os.Stderr))
}
