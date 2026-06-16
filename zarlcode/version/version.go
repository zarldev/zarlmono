// Package version reports the zarlcode build version. It's a leaf so
// both the entry (zarlcode -version) and the TUI intro can read the
// build identity without importing each other.
package version

import (
	"runtime/debug"
	"strings"
)

const develVersion = "(devel)"

// version is the build-time-injectable version string. Leave the
// default empty so String() falls through to vcs metadata — devs
// running `go run` or `go install` without ldflags still get a useful
// identifier (the short git hash + dirty marker) instead of a
// hardcoded sentinel.
//
// Override at build time:
//
//	go build -ldflags "-X github.com/zarldev/zarlmono/zarlcode/version.version=v1.2.3" ./zarlcode/cmd
//
// The root Taskfile's zarlcode task injects `git describe --tags --dirty`.
var version = ""

// String returns the version to display on the intro and via
// `zarlcode -version`. Resolution order:
//
//  1. The ldflags-injected `version` var (Taskfile / release builds).
//  2. The Go-module Main.Version when installed via `go install
//     module@version` (turns into something like "v0.1.0").
//  3. The VCS short revision from build info, suffixed with "-dirty"
//     if the working tree had uncommitted changes at build time.
//  4. The literal develVersion — last resort for `go run` and similar.
func String() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	if v := strings.TrimSpace(info.Main.Version); v != "" && v != develVersion {
		return v
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" {
		short := rev
		if len(short) > 7 {
			short = short[:7]
		}
		if modified == "true" {
			short += "-dirty"
		}
		return short
	}
	return develVersion
}
