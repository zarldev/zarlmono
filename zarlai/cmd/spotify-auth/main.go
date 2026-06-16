// spotify-auth runs the Spotify OAuth authorisation-code flow and
// writes a token cache file. The cache shape matches Python's spotipy
// CacheFileHandler so any MCP client expecting that format can read it
// without translation.
//
// Reads spotify client_id / client_secret / redirect_uri / cache_path
// from tool_providers.config (keyed on name='spotify'). Populate those
// via the admin UI before running this tool. Only env var read is
// DOLT_DSN.
//
// Usage:
//
//	export DOLT_DSN='root:@tcp(localhost:3307)/zarl?parseTime=true'
//	go run ./cmd/spotify-auth
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// scopes must be a SUPERSET of every scope the consuming MCP client
// requests at runtime: spotipy's SpotifyOAuth invalidates the cached
// token and triggers a fresh browser flow if it ever sees a missing
// scope. Adding a scope here is cheap; missing one burns the cache.
var scopes = []string{
	"user-library-read",
	"user-library-modify",
	"user-modify-playback-state",
	"user-read-playback-state",
	"user-read-currently-playing",
	"user-read-recently-played",
	"user-read-playback-position",
	"user-top-read",
	"playlist-read-private",
	"playlist-read-collaborative",
	"playlist-modify-public",
	"playlist-modify-private",
}

// tokenCache mirrors spotipy's CacheFileHandler on-disk JSON shape.
type tokenCache struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	ExpiresAt    int64  `json:"expires_at"`
}

func main() {
	dsn := os.Getenv("DOLT_DSN")
	if dsn == "" {
		log.Fatal("DOLT_DSN must be set (same value the main zarl binary uses)")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var rawConfig string
	err = db.QueryRowContext(context.Background(),
		"SELECT config FROM tool_providers WHERE name = 'spotify'",
	).Scan(&rawConfig)
	if err != nil {
		log.Fatalf("load spotify provider config: %v", err)
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(rawConfig), &cfg); err != nil {
		log.Fatalf("parse spotify config: %v", err)
	}
	clientID := cfg["client_id"]
	clientSecret := cfg["client_secret"]
	redirectURI := cfg["redirect_uri"]
	cachePath := cfg["cache_path"]
	if clientID == "" || clientSecret == "" || redirectURI == "" || cachePath == "" {
		log.Fatal("spotify tool_providers.config is missing client_id / client_secret / redirect_uri / cache_path — populate via the admin UI first")
	}

	redirect, err := url.Parse(redirectURI)
	if err != nil {
		log.Fatalf("parse redirect URI: %v", err)
	}
	if redirect.Host == "" {
		log.Fatalf("redirect URI has no host:port — got %q", redirectURI)
	}

	// CSRF-protection state. Spotify echoes it back on the callback;
	// we check before accepting the code.
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		log.Fatalf("rand: %v", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	authURL := "https://accounts.spotify.com/authorize?" + url.Values{
		"client_id":     {clientID},
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"state":         {state},
		"scope":         {strings.Join(scopes, " ")},
	}.Encode()

	// Local HTTP server catches the Spotify redirect. The path on the
	// callback URL is whatever the operator registered — we bind that
	// path, not a fixed one.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	path := redirect.Path
	if path == "" {
		path = "/"
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			fmt.Fprintf(w, "auth failed: %s — check your terminal.", e)
			errCh <- fmt.Errorf("spotify returned error: %s", e)
			return
		}
		if got := q.Get("state"); got != state {
			fmt.Fprint(w, "csrf state mismatch — auth aborted.")
			errCh <- fmt.Errorf("csrf state mismatch (got %q)", got)
			return
		}
		code := q.Get("code")
		if code == "" {
			fmt.Fprint(w, "no code in callback.")
			errCh <- fmt.Errorf("no code in callback")
			return
		}
		fmt.Fprint(w, "✓ Spotify auth complete. You can close this tab and return to the terminal.")
		codeCh <- code
	})
	srv := &http.Server{Addr: redirect.Host, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	fmt.Println("→ Spotify OAuth")
	fmt.Printf("  callback server : %s\n", redirect.Host)
	fmt.Printf("  token cache     : %s\n", cachePath)
	fmt.Println()
	fmt.Println("  Open this URL in any browser and approve:")
	fmt.Println()
	fmt.Println("  " + authURL)
	fmt.Println()

	// Best-effort browser launcher. Prints the URL regardless so
	// headless users always have the option of opening it manually.
	tryOpenBrowser(authURL)

	var code string
	select {
	case c := <-codeCh:
		code = c
	case err := <-errCh:
		log.Fatalf("auth failed: %v", err)
	case <-time.After(5 * time.Minute):
		log.Fatal("timed out waiting for Spotify callback after 5 minutes")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = srv.Shutdown(ctx)
	cancel()

	tok, err := exchangeCode(clientID, clientSecret, code, redirectURI)
	if err != nil {
		log.Fatalf("exchange code for token: %v", err)
	}
	tok.ExpiresAt = time.Now().Unix() + int64(tok.ExpiresIn)
	if tok.Scope == "" {
		// Spotify echoes the scope it actually granted — if it's
		// empty (it shouldn't be), fall back to what we asked for so
		// spotipy has something to write in the cache.
		tok.Scope = strings.Join(scopes, " ")
	}

	if err := writeCache(cachePath, tok); err != nil {
		log.Fatalf("write cache: %v", err)
	}
	fmt.Printf("\n✓ auth complete. refresh token cached at %s\n", cachePath)
}

// exchangeCode POSTs to Spotify's token endpoint with the auth code
// obtained from the redirect. Uses HTTP Basic auth (client_id:secret)
// as Spotify requires for server-side flows.
func exchangeCode(clientID, clientSecret, code, redirectURI string) (*tokenCache, error) {
	body := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	req, err := http.NewRequest(http.MethodPost, "https://accounts.spotify.com/api/token", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var tok tokenCache
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("decode token response: %w (body: %s)", err, string(raw))
	}
	return &tok, nil
}

// writeCache writes the token file with 0600 perms at cachePath.
// Creates the parent directory if missing.
func writeCache(path string, tok *tokenCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// tryOpenBrowser launches the user's default browser in the background
// with the auth URL. Silently no-ops in headless environments — the URL
// is always printed so manual open works.
func tryOpenBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", u)
	default:
		if _, err := exec.LookPath("wslview"); err == nil {
			cmd = exec.Command("wslview", u)
		} else if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", u)
		}
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}
