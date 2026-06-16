package filesystem

import (
	"io/fs"
	"testing"
)

func TestFileModeFromPermissionBits(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		mode int
		want fs.FileMode
	}{
		"negative":        {mode: -1, want: 0},
		"zero":            {mode: 0, want: 0},
		"regular file":    {mode: 0o644, want: 0o644},
		"executable":      {mode: 0o755, want: 0o755},
		"seaweed regular": {mode: 432, want: 0o660},
		"ignores high bits": {
			mode: 0o100000 | 0o755,
			want: 0o755,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := fileModeFromPermissionBits(tc.mode); got != tc.want {
				t.Fatalf("fileModeFromPermissionBits(%#o) = %#o, want %#o", tc.mode, got, tc.want)
			}
		})
	}
}
