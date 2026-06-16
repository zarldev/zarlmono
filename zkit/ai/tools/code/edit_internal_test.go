package code

import "testing"

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
			old:  "foo",
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
			start, end, hits := fuzzyMatch(tt.body, tt.old)
			if hits != tt.hits {
				t.Fatalf("hits = %d, want %d", hits, tt.hits)
			}
			if hits != 1 {
				return // start/end only meaningful on a unique match
			}
			got := tt.body[:start] + repl + tt.body[end:]
			if got != tt.want {
				t.Errorf("splice = %q, want %q (start=%d end=%d)", got, tt.want, start, end)
			}
		})
	}
}
