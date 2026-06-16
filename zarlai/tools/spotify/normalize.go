package spotify

import (
	"fmt"
	"net/url"
	"strings"
)

// validKinds is the set of Spotify resource kinds we accept. Shows and
// episodes are deliberately excluded — playback semantics differ and
// we don't expose tools for them yet.
var validKinds = map[string]struct{}{
	"track":    {},
	"album":    {},
	"artist":   {},
	"playlist": {},
}

// NormalizeID parses any of the forms an LLM or user might emit for a
// Spotify resource and returns the canonical URI plus the resource
// kind.
//
// Accepted inputs, any of which may be surrounded by whitespace or
// quote characters (' or "):
//   - bare 22-char base62 ID ("5gR6gTGOGsg9zcR7JhvwQz") — assumed track
//   - spotify:TYPE:ID URI
//   - https://open.spotify.com/TYPE/ID[?query]
//
// The leading quote-strip absorbs the literal quote chars some
// tool-calling LLMs wrap string arguments in.
func NormalizeID(in string) (uri, kind string, err error) {
	s := strings.TrimSpace(in)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "null") {
		return "", "", fmt.Errorf("spotify id: empty")
	}

	switch {
	case strings.HasPrefix(s, "spotify:"):
		parts := strings.Split(s, ":")
		if len(parts) != 3 {
			return "", "", fmt.Errorf("spotify id %q: expected spotify:TYPE:ID", in)
		}
		kind = parts[1]
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		u, perr := url.Parse(s)
		if perr != nil {
			return "", "", fmt.Errorf("spotify id %q: %w", in, perr)
		}
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) < 2 {
			return "", "", fmt.Errorf("spotify id %q: url missing type/id", in)
		}
		kind = segs[0]
		s = fmt.Sprintf("spotify:%s:%s", segs[0], segs[1])
	default:
		kind = "track"
		s = "spotify:track:" + s
	}

	if _, ok := validKinds[kind]; !ok {
		return "", "", fmt.Errorf("spotify id %q: unsupported kind %q", in, kind)
	}
	return s, kind, nil
}
