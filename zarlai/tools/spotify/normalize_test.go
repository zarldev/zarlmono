package spotify_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/tools/spotify"
)

func TestNormalizeID(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantURI  string
		wantKind string
		wantErr  bool
	}{
		{"bare id defaults to track", "5gR6gTGOGsg9zcR7JhvwQz", "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"spotify uri track", "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"spotify uri album", "spotify:album:1abc", "spotify:album:1abc", "album", false},
		{"spotify uri artist", "spotify:artist:2def", "spotify:artist:2def", "artist", false},
		{"spotify uri playlist", "spotify:playlist:3ghi", "spotify:playlist:3ghi", "playlist", false},
		{"open.spotify.com track", "https://open.spotify.com/track/5gR6gTGOGsg9zcR7JhvwQz", "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"open.spotify.com with query", "https://open.spotify.com/track/5gR6gTGOGsg9zcR7JhvwQz?si=xyz", "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"double-quote wrapped", `"spotify:track:5gR6gTGOGsg9zcR7JhvwQz"`, "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"single-quote wrapped", `'spotify:track:5gR6gTGOGsg9zcR7JhvwQz'`, "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"leading and trailing whitespace", "  spotify:track:5gR6gTGOGsg9zcR7JhvwQz  ", "spotify:track:5gR6gTGOGsg9zcR7JhvwQz", "track", false},
		{"empty", "", "", "", true},
		{"literal null string", "null", "", "", true},
		{"unknown kind", "spotify:show:abc", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uri, kind, err := spotify.NormalizeID(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if uri != tc.wantURI {
				t.Errorf("uri = %q, want %q", uri, tc.wantURI)
			}
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", kind, tc.wantKind)
			}
		})
	}
}
