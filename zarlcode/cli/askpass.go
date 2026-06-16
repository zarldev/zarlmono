package cli

// Sudo askpass client.
//
// The bash tool spawns children with Setsid (no controlling TTY) so
// programs like sudo can't read passwords directly from /dev/tty.
// To still let `sudo -A <cmd>` work, the interactive shell provides an
// askpass helper that talks back to the shell process — which DOES have
// the TTY — over a unix socket.
//
//	zarlcode --askpass "<prompt>"  — the helper mode. Sudo execs
//	                                    this with the prompt as argv[1].
//	                                    We connect to the socket whose
//	                                    path is in env, send the prompt,
//	                                    read the password back, and print
//	                                    it to stdout (sudo's contract).
//
// The server side (the in-TUI password prompt that answers these
// requests) is a TUI feature; it lives with the interactive shell.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
)

// RunAskpassClient is the entry point for `zarlcode --askpass`.
// Connects to the shell over the unix socket and pipes the password
// back to sudo via stdout. Never returns to main(); calls os.Exit.
func RunAskpassClient(args []string) {
	os.Exit(runAskpassClient(args))
}

func runAskpassClient(args []string) int {
	sock := os.Getenv(askpass.EnvSock)
	if sock == "" {
		fmt.Fprintln(os.Stderr, "zarlcode-askpass: ZARLCODE_ASKPASS_SOCK is unset")
		return 2
	}
	prompt := "Password:"
	if len(args) > 0 {
		prompt = strings.TrimSpace(strings.Join(args, " "))
	}

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zarlcode-askpass: dial:", err)
		return 2
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(askpass.Request{Prompt: prompt}); err != nil {
		fmt.Fprintln(os.Stderr, "zarlcode-askpass: send:", err)
		return 2
	}
	var resp askpass.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		fmt.Fprintln(os.Stderr, "zarlcode-askpass: recv:", err)
		return 2
	}
	if resp.Error != "" {
		fmt.Fprintln(os.Stderr, "zarlcode-askpass:", resp.Error)
		return 2
	}
	// sudo reads exactly one line from stdout — no trailing newline
	// quirks needed; Println adds the newline sudo expects.
	fmt.Fprintln(os.Stdout, resp.Password)
	return 0
}
