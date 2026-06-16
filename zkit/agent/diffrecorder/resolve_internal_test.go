package diffrecorder

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveUnderRoot_RelativeEscapeRejected guards the path
// traversal that the adversarial review flagged (docs/adversarial-
// repo-review.md, finding #1): a tool-supplied relative path
// containing `..` segments used to slip through filepath.Join
// unchecked, letting snapshot reads land outside the workspace.
//
// Both absolute and relative inputs are now run through the same
// lexical containment check after normalisation. The cases cover
// the escape vectors plus the legitimate paths that have to keep
// working — sibling dirs, root itself, leading-slash absolutes,
// and the no-op cleanup forms (`./foo`, `foo/./bar`).
func TestResolveUnderRoot_RelativeEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path semantics; the Recorder runs on linux/darwin")
	}
	const root = "/ws"
	cases := []struct {
		name    string
		input   string
		want    string // expected resolved absolute path; "" when reject expected
		wantErr bool
	}{
		// Legitimate paths — must keep working.
		{"relative simple", "src/main.go", "/ws/src/main.go", false},
		{"relative dot prefix", "./README.md", "/ws/README.md", false},
		{"relative dot mid path", "src/./main.go", "/ws/src/main.go", false},
		{"absolute inside root", "/ws/src/main.go", "/ws/src/main.go", false},
		{"root itself absolute", "/ws", "/ws", false},
		// Escape vectors — must reject.
		{"relative parent walk", "../../etc/passwd", "", true},
		{"relative dot-dot inside", "src/../../etc/passwd", "", true},
		{"absolute outside root", "/etc/passwd", "", true},
		{"absolute sibling prefix", "/ws_other/file", "", true},
		// Edge cases the lexical check has to handle.
		{"trailing slash on input", "src/", "/ws/src", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveUnderRoot(root, c.input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveUnderRoot(%q, %q) = %q, nil; want error", root, c.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveUnderRoot(%q, %q) returned %v; want %q", root, c.input, err, c.want)
			}
			if got != filepath.Clean(c.want) {
				t.Fatalf("resolveUnderRoot(%q, %q) = %q; want %q", root, c.input, got, c.want)
			}
		})
	}
}

// TestResolveUnderRoot_UncleanRootStillContains covers the
// "root passed in with a trailing slash" case — without the
// filepath.Clean on root inside the helper, the prefix comparison
// would compare "/ws/" against "/ws/file" and tolerate "/wstest"
// as an in-root path (false negative on a near-miss).
func TestResolveUnderRoot_UncleanRootStillContains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path semantics; the Recorder runs on linux/darwin")
	}
	got, err := resolveUnderRoot("/ws/", "src/main.go")
	if err != nil {
		t.Fatalf("resolveUnderRoot with trailing-slash root errored: %v", err)
	}
	if got != "/ws/src/main.go" {
		t.Fatalf("resolveUnderRoot trailing-slash root = %q; want /ws/src/main.go", got)
	}
	// Near-miss: "/wsneighbor" must NOT be accepted just because it
	// starts with "/ws". The cleaned-root + separator check is what
	// catches this.
	_, err = resolveUnderRoot("/ws", "/wsneighbor/file")
	if err == nil {
		t.Fatalf("expected error for /wsneighbor under /ws — prefix-only check would let this through")
	}
}
