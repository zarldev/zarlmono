package sensor

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"

	"github.com/zarldev/zarlmono/zarlai/tools/spotify"
)

// SpotifyNowPlayingFetcher is the subset of spotify.Client the sensor needs.
// Kept narrow so tests can fake it.
type SpotifyNowPlayingFetcher interface {
	NowPlaying(ctx context.Context) (spotify.NowPlayingInfo, error)
}

// SpotifyNowPlaying polls Spotify every 10s and emits on track or
// play/pause changes. The Detail field carries a JSON-encoded payload
// the frontend parses directly ({track, artist, album, device,
// is_playing}). When no track is active, Track is "" and the UI hides
// the now-playing strip.
func SpotifyNowPlaying(client SpotifyNowPlayingFetcher) *zsensor.Func {
	return zsensor.NewFunc("spotify_now_playing", 10*time.Second, func(ctx context.Context) (zsensor.Observation, error) {
		info, err := client.NowPlaying(ctx)
		if err != nil {
			return zsensor.Observation{}, err
		}
		// Composite change key: emit whenever track, artist, or play
		// state flips. Pausing/resuming the same track still broadcasts
		// so the UI can flip the play indicator.
		key := info.Track + "|" + info.Artist + "|" + strconv.FormatBool(info.IsPlaying)
		payload, err := json.Marshal(struct {
			Track     string `json:"track"`
			Artist    string `json:"artist"`
			Album     string `json:"album"`
			Device    string `json:"device"`
			ImageURL  string `json:"image_url,omitempty"`
			IsPlaying bool   `json:"is_playing"`
		}{info.Track, info.Artist, info.Album, info.Device, info.ImageURL, info.IsPlaying})
		if err != nil {
			return zsensor.Observation{}, err
		}
		return zsensor.Observation{
			Value:  key,
			Detail: string(payload),
		}, nil
	})
}
