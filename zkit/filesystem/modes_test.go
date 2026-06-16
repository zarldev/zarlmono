package filesystem_test

import (
	"io/fs"
	"testing"

	"github.com/zarldev/zarlmono/zkit/filesystem"
)

func TestModeConstantsDocumentExpectedPermissions(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		got  fs.FileMode
		want fs.FileMode
	}{
		"private dir":     {got: filesystem.ModePrivateDir, want: 0o700},
		"private file":    {got: filesystem.ModePrivateFile, want: 0o600},
		"shared dir":      {got: filesystem.ModeSharedDir, want: 0o750},
		"public dir":      {got: filesystem.ModePublicDir, want: 0o755},
		"public file":     {got: filesystem.ModePublicFile, want: 0o644},
		"executable file": {got: filesystem.ModeExecutableFile, want: 0o755},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("mode = %v, want %v", tc.got, tc.want)
			}
		})
	}
}
