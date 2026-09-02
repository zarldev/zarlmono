// Package version reports the sweeval build identity.
package version

import (
	"runtime/debug"
	"strings"
)

const develVersion = "(devel)"

var version string

// String returns the build version from ldflags, module metadata, or VCS data.
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
	var revision string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	if revision == "" {
		return develVersion
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}
