package searxng

import "testing"

func TestExtractYouTubeID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=10s", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?t=5", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://example.com/watch?v=dQw4w9WgXcQ", ""},
		{"https://youtube.com/", ""},
		{"https://youtube.com/watch?v=tooshort", ""},
		{"not a url", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := extractYouTubeID(tc.url); got != tc.want {
				t.Errorf("extractYouTubeID(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
