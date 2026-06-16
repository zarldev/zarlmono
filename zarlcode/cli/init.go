package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

// RunInit is the entry point for `zarlcode init`. It
// runs the same materialisation the implicit first-run does, but
// prints the result and never opens a TUI. Idempotent — a second
// `zarlcode init` is a fast scan that reports "everything
// existed".
func RunInit(stdout io.Writer) int {
	res, err := home.Materialise()
	if err != nil {
		fmt.Fprintln(os.Stderr, "init:", err)
		return 1
	}
	fmt.Fprint(stdout, res.String())
	return 0
}
