// Command zarlcode is the entrypoint for the zarlcode TUI. The
// implementation lives in the parent package; this binary is a thin
// shim so the command path (zarlcode/cmd) stays separate from the
// library package (zarlcode).
//
// See package zarlcode for the full command reference and the
// behaviour of subcommands (init, keys, upgrade) and flags
// (-continue, -headless, --askpass, …).
package main

import "github.com/zarldev/zarlmono/zarlcode"

func main() {
	zarlcode.Main()
}
