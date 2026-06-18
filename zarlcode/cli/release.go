package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// githubAPIBase is the GitHub REST root. It's a var so tests can point the
// release client at an httptest server.
var githubAPIBase = "https://api.github.com"

// currentGOOS / currentGOARCH report the running platform. They're vars so
// tests can pretend to be another platform without cross-compiling.
var (
	currentGOOS   = func() string { return runtime.GOOS }
	currentGOARCH = func() string { return runtime.GOARCH }
)

// binaryName is the executable packed inside each release archive — see the
// release workflow, which builds `zarlcode` (`.exe` on windows).
func binaryName(goos string) string {
	if goos == "windows" {
		return "zarlcode.exe"
	}
	return "zarlcode"
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// fetchRelease resolves a release from the GitHub API. An empty or "latest"
// version returns the newest published release; anything else is treated as a
// tag name.
func fetchRelease(ctx context.Context, repo, version string) (ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, repo)
	if v := strings.TrimSpace(version); v != "" && v != "latest" {
		url = fmt.Sprintf("%s/repos/%s/releases/tags/%s", githubAPIBase, repo, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ghRelease{}, fmt.Errorf("build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	authorizeGitHub(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ghRelease{}, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("fetch release %s: github responded %s", repo, resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return ghRelease{}, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return ghRelease{}, fmt.Errorf("release for %s has no tag", repo)
	}
	return rel, nil
}

// selectAssets finds the platform archive and the checksums manifest in a
// release. The archive name follows the workflow's contract
// `zarlcode_<version>_<goos>_<goarch>.{tar.gz,zip}`, so matching on the
// `_<goos>_<goarch>.` infix needs no os/arch translation table.
func selectAssets(rel ghRelease, goos, goarch string) (archive, checksums ghAsset, err error) {
	infix := fmt.Sprintf("_%s_%s.", goos, goarch)
	for _, a := range rel.Assets {
		switch {
		case a.Name == checksumsAsset:
			checksums = a
		case strings.Contains(a.Name, infix) && isArchiveName(a.Name):
			archive = a
		}
	}
	if archive.URL == "" {
		return archive, checksums, fmt.Errorf("release %s has no zarlcode asset for %s/%s", rel.TagName, goos, goarch)
	}
	if checksums.URL == "" {
		return archive, checksums, fmt.Errorf("release %s is missing %s", rel.TagName, checksumsAsset)
	}
	return archive, checksums, nil
}

const checksumsAsset = "checksums.txt"

func isArchiveName(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip")
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	authorizeGitHub(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: server responded %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}
	return b, nil
}

// authorizeGitHub attaches a bearer token when GITHUB_TOKEN is set so private
// repos and rate-limited environments work; public release downloads need none.
func authorizeGitHub(req *http.Request) {
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// verifyChecksum confirms the downloaded archive matches its line in the
// sha256sum-format manifest. A missing or mismatched entry is fatal — we never
// install an unverified binary.
func verifyChecksum(archiveName string, archive, manifest []byte) error {
	want := ""
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum prefixes the name with '*' in binary mode; tolerate it.
		if strings.TrimPrefix(fields[1], "*") == archiveName {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum listed for %s", archiveName)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", archiveName, got, want)
	}
	return nil
}

func extractBinary(archiveName string, data []byte, goos string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return extractZip(data, goos)
	}
	return extractTarGz(data, goos)
}

func extractTarGz(data []byte, goos string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	want := binaryName(goos)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if filepath.Base(hdr.Name) != want {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s from tar: %w", want, err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("archive has no %s entry", want)
}

func extractZip(data []byte, goos string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	want := binaryName(goos)
	for _, f := range zr.File {
		if filepath.Base(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in zip: %w", want, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s from zip: %w", want, err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("archive has no %s entry", want)
}

// installBinary atomically replaces the file at binPath. It writes to a temp
// file in the same directory (so the rename can't cross filesystems) and renames
// over the target; on windows the running executable is moved aside first since
// it can't be replaced in place.
func installBinary(binPath string, data []byte, goos string) error {
	dir := filepath.Dir(binPath)
	tmp, err := os.CreateTemp(dir, ".zarlcode-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod temp binary: %w", err)
	}
	if goos == "windows" {
		_ = os.Rename(binPath, binPath+".old")
	}
	if err := os.Rename(tmpName, binPath); err != nil {
		return fmt.Errorf("install binary to %s: %w", binPath, err)
	}
	return nil
}
