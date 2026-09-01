package code_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// TestFuzzyMatchBoundary locks the byte-range math of the whitespace-normalised
// edit fallback — specifically the trailing-newline / CRLF exclusion that the
// audit flagged as subtle. Each case reconstructs the splice
// (body[:start] + repl + body[end:]) and checks the exact result.
func TestFuzzyMatchBoundary(t *testing.T) {
	const repl = "X"
	cases := []struct {
		name string
		body string
		old  string
		want string // result of splicing repl over [start,end)
		hits int
	}{
		{
			name: "no trailing newline preserves the line's \\n",
			body: "foo\nbar\nbaz\n",
			old:  "bar", // no trailing newline
			want: "foo\nX\nbaz\n",
			hits: 1,
		},
		{
			name: "CRLF: excludes both \\r and \\n",
			body: "foo\r\nbar\r\n",
			old:  "bar",
			want: "foo\r\nX\r\n",
			hits: 1,
		},
		{
			name: "old WITH trailing newline consumes the \\n",
			body: "foo\nbar\nbaz",
			old:  "bar\n",
			want: "foo\nXbaz",
			hits: 1,
		},
		{
			name: "trailing whitespace on body line still matches",
			body: "foo  \nbar\n",
			old:  "foo\t",
			want: "X\nbar\n",
			hits: 1,
		},
		{
			name: "last line, no EOL anywhere",
			body: "foo\nbar",
			old:  "bar",
			want: "foo\nX",
			hits: 1,
		},
		{
			name: "ambiguous match is refused",
			body: "dup\ndup\n",
			old:  "dup",
			hits: 2,
		},
		{
			name: "no match",
			body: "foo\nbar\n",
			old:  "zzz",
			hits: 0,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "f"), []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			ws, err := code.NewWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			res, err := code.NewEditTool(ws).Execute(t.Context(), tools.ToolCall{ID: "test", Arguments: tools.ToolParameters{"path": "f", "old_string": tt.old, "new_string": repl}})
			if err != nil {
				t.Fatal(err)
			}
			if tt.hits != 1 {
				if res.Success {
					t.Fatalf("expected refusal for %d matches", tt.hits)
				}
				return
			}
			if !res.Success {
				t.Fatalf("edit: %s", res.Error)
			}
			got, err := os.ReadFile(filepath.Join(root, "f"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("splice = %q, want %q", got, tt.want)
			}
		})
	}
}
