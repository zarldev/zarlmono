package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/prefs"
)

// makeReleaseArchive builds a tar.gz holding a fake zarlcode binary and returns
// its name, bytes, and lowercase sha256 — matching the workflow's packaging.
func makeReleaseArchive(t *testing.T, tag, goos, goarch string) (string, []byte, string) {
	t.Helper()
	var tarBuf bytes.Buffer
	gz := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gz)
	body := []byte("fake-zarlcode-" + tag + "-" + goos + "-" + goarch)
	bin := binaryName(goos)
	if err := tw.WriteHeader(&tar.Header{Name: bin, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	data := tarBuf.Bytes()
	h := sha256.Sum256(data)
	name := fmt.Sprintf("zarlcode_%s_%s_%s.tar.gz", tag, goos, goarch)
	return name, data, hex.EncodeToString(h[:])
}

// newReleaseServer serves the GitHub release API (list + by-tag) and asset
// downloads for one release, pointing the package's release client at it. tag is
// the full submodule tag, e.g. "zarlcode/v1.2.3".
func newReleaseServer(t *testing.T, tag string, assets map[string][]byte) {
	newReleaseServerWithTagPath(t, tag, assets, nil)
}

func newReleaseServerWithTagPath(t *testing.T, tag string, assets map[string][]byte, tagPathSeen *string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release := func() ghRelease {
			rel := ghRelease{TagName: tag}
			for name := range assets {
				rel.Assets = append(rel.Assets, ghAsset{
					Name: name,
					URL:  "http://" + r.Host + "/dl/" + name,
				})
			}
			return rel
		}
		switch {
		case strings.Contains(r.URL.EscapedPath(), "/releases/tags/"):
			gotPath := strings.TrimPrefix(r.URL.EscapedPath(), "/repos/acme/tool/releases/tags/")
			if tagPathSeen != nil {
				*tagPathSeen = gotPath
			}
			if gotPath != url.PathEscape(tag) {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(release())
		case strings.HasSuffix(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]ghRelease{release()})
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			body, ok := assets[path.Base(r.URL.Path)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = oldBase })
}

func fakePlatform(t *testing.T, goarch string) {
	t.Helper()
	oldOS, oldArch := currentGOOS, currentGOARCH
	currentGOOS = func() string { return "linux" }
	currentGOARCH = func() string { return goarch }
	t.Cleanup(func() { currentGOOS, currentGOARCH = oldOS, oldArch })
}

func TestValidateUpgradeSource(t *testing.T) {
	for _, ok := range []string{"zarldev/zarlmono", "acme/tool-1", "a.b/c_d"} {
		if err := validateUpgradeSource(ok); err != nil {
			t.Errorf("validateUpgradeSource(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "noslash", "/leading/extra", "owner/repo/extra", "owner /repo"} {
		if err := validateUpgradeSource(bad); err == nil {
			t.Errorf("validateUpgradeSource(%q) = nil, want error", bad)
		}
	}
}

func TestNormalizeUpgradeSource(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"zarldev/zarlmono", "zarldev/zarlmono"},
		{"https://github.com/zarldev/zarlmono", "zarldev/zarlmono"},
		{"https://github.com/zarldev/zarlmono.git", "zarldev/zarlmono"},
		{"github.com/acme/tool/", "acme/tool"},
	} {
		got, err := normalizeUpgradeSource(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("normalizeUpgradeSource(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestUpgradeSettingsCommandsPersistGlobally(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	bin := filepath.Join(t.TempDir(), "zarlcode")
	var out, errOut bytes.Buffer

	if code := runUpgradeWithService(ctx, svc, []string{"source", "set", "acme/tool"}, &out, &errOut, false); code != 0 {
		t.Fatalf("source set exit %d stderr=%q", code, errOut.String())
	}
	got, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource)
	if err != nil || !ok || got.Value != "acme/tool" {
		t.Fatalf("upgrade_source = %+v ok=%v err=%v, want %q", got, ok, err, "acme/tool")
	}

	out.Reset()
	errOut.Reset()
	if code := runUpgradeWithService(ctx, svc, []string{"bin-path", "set", bin}, &out, &errOut, false); code != 0 {
		t.Fatalf("bin-path set exit %d stderr=%q", code, errOut.String())
	}
	got, ok, err = svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath)
	if err != nil || !ok || got.Value != bin {
		t.Fatalf("upgrade_bin_path = %+v ok=%v err=%v, want %q", got, ok, err, bin)
	}

	for _, tc := range []struct {
		args []string
		key  string
		want string
	}{
		{[]string{"restart", "set", "true"}, settingKeyUpgradeRestart, "true"},
		{[]string{"dry-run", "set", "true"}, settingKeyUpgradeDryRun, "true"},
	} {
		out.Reset()
		errOut.Reset()
		if code := runUpgradeWithService(ctx, svc, tc.args, &out, &errOut, false); code != 0 {
			t.Fatalf("%v exit %d stderr=%q", tc.args, code, errOut.String())
		}
		got, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, tc.key)
		if err != nil || !ok || got.Value != tc.want {
			t.Fatalf("%s = %+v ok=%v err=%v, want %q", tc.key, got, ok, err, tc.want)
		}
	}

	out.Reset()
	errOut.Reset()
	if code := runUpgradeWithService(ctx, svc, []string{"source", "clear"}, &out, &errOut, false); code != 0 {
		t.Fatalf("source clear exit %d stderr=%q", code, errOut.String())
	}
	if _, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource); err != nil || ok {
		t.Fatalf("source clear left row ok=%v err=%v", ok, err)
	}
}

func TestResolveUpgradeRepoMigratesLegacySourcePath(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	legacyPath := filepath.Join(t.TempDir(), "zarlmono")
	if err := svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, legacyPath); err != nil {
		t.Fatalf("set legacy source: %v", err)
	}

	repo, err := resolveUpgradeRepo(ctx, svc)
	if err != nil {
		t.Fatalf("resolve repo: %v", err)
	}
	if repo != defaultUpgradeRepo {
		t.Fatalf("repo = %q, want default %q", repo, defaultUpgradeRepo)
	}
	if _, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource); err != nil || ok {
		t.Fatalf("legacy source not cleared: ok=%v err=%v", ok, err)
	}
}

func TestRunUpgradeDryRunDoesNotDownload(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	fakePlatform(t, "amd64")

	name, data, sum := makeReleaseArchive(t, "v1.2.3", "linux", "amd64")
	newReleaseServer(t, "zarlcode/v1.2.3", map[string][]byte{
		name:           data,
		checksumsAsset: []byte(sum + "  " + name + "\n"),
	})

	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, "acme/tool")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	res, err := runUpgrade(ctx, svc, upgradeOptions{DryRun: true, DryRunOverride: true})
	if err != nil {
		t.Fatalf("runUpgrade dry-run: %v", err)
	}
	if !res.DryRun || res.Version != "v1.2.3" || res.AssetName != name || res.Repo != "acme/tool" {
		t.Fatalf("unexpected dry-run result: %+v", res)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a binary (stat err=%v)", err)
	}
}

func TestRunUpgradeInstallsReleaseBinary(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	fakePlatform(t, "amd64")

	name, data, sum := makeReleaseArchive(t, "v2.0.0", "linux", "amd64")
	// A decoy for another platform must be ignored.
	otherName, otherData, otherSum := makeReleaseArchive(t, "v2.0.0", "darwin", "arm64")
	newReleaseServer(t, "zarlcode/v2.0.0", map[string][]byte{
		name:           data,
		otherName:      otherData,
		checksumsAsset: []byte(sum + "  " + name + "\n" + otherSum + "  " + otherName + "\n"),
	})

	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, "acme/tool")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	res, err := runUpgrade(ctx, svc, upgradeOptions{})
	if err != nil {
		t.Fatalf("runUpgrade: %v", err)
	}
	if res.Version != "v2.0.0" || res.UpToDate {
		t.Fatalf("unexpected result: %+v", res)
	}
	installed, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(installed) != "fake-zarlcode-v2.0.0-linux-amd64" {
		t.Fatalf("installed wrong binary: %q", installed)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary not executable: %v", info.Mode())
	}
}

func TestRunUpgradeRejectsChecksumMismatch(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	fakePlatform(t, "amd64")

	name, data, _ := makeReleaseArchive(t, "v1.0.0", "linux", "amd64")
	newReleaseServer(t, "zarlcode/v1.0.0", map[string][]byte{
		name:           data,
		checksumsAsset: []byte(strings.Repeat("0", 64) + "  " + name + "\n"),
	})

	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, "acme/tool")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	if _, err := runUpgrade(ctx, svc, upgradeOptions{}); err == nil ||
		!strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("mismatch still installed a binary (stat err=%v)", err)
	}
}

func TestRunUpgradeErrorsWhenNoAssetForPlatform(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	fakePlatform(t, "arm64")

	name, data, sum := makeReleaseArchive(t, "v1.0.0", "linux", "amd64") // only amd64 published
	newReleaseServer(t, "zarlcode/v1.0.0", map[string][]byte{
		name:           data,
		checksumsAsset: []byte(sum + "  " + name + "\n"),
	})
	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, "acme/tool")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	if _, err := runUpgrade(ctx, svc, upgradeOptions{}); err == nil ||
		!strings.Contains(err.Error(), "no installable acme/tool release for linux/arm64") {
		t.Fatalf("err = %v, want missing-asset error", err)
	}
}

func TestRunUpgradeInstallsPinnedVersion(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	fakePlatform(t, "amd64")

	name, data, sum := makeReleaseArchive(t, "v1.5.0", "linux", "amd64")
	var tagPathSeen string
	newReleaseServerWithTagPath(t, "zarlcode/v1.5.0", map[string][]byte{
		name:           data,
		checksumsAsset: []byte(sum + "  " + name + "\n"),
	}, &tagPathSeen)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, "acme/tool")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)
	// --version accepts the bare version; the client re-adds the submodule
	// prefix to resolve the tag zarlcode/v1.5.0.
	res, err := runUpgrade(ctx, svc, upgradeOptions{Version: "v1.5.0"})
	if err != nil {
		t.Fatalf("runUpgrade pinned: %v", err)
	}
	if res.Version != "v1.5.0" {
		t.Fatalf("res.Version = %q, want v1.5.0", res.Version)
	}
	if tagPathSeen != url.PathEscape("zarlcode/v1.5.0") {
		t.Fatalf("tag path = %q, want escaped submodule tag", tagPathSeen)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("pinned upgrade did not install: %v", err)
	}
}

func TestUpgradeRestartExecsInstalledBinary(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	svc := prefs.NewService(store, nil, "")
	fakePlatform(t, "amd64")

	name, data, sum := makeReleaseArchive(t, "v3.0.0", "linux", "amd64")
	newReleaseServer(t, "zarlcode/v3.0.0", map[string][]byte{
		name:           data,
		checksumsAsset: []byte(sum + "  " + name + "\n"),
	})
	bin := filepath.Join(t.TempDir(), "zarlcode")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, "acme/tool")
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, bin)

	oldExec := execUpgradeBinary
	var execPath string
	execUpgradeBinary = func(path string, argv, env []string) error {
		execPath = path
		return nil
	}
	t.Cleanup(func() { execUpgradeBinary = oldExec })

	var out, errOut bytes.Buffer
	if code := runUpgradeWithService(ctx, svc, []string{"--restart"}, &out, &errOut, true); code != 0 {
		t.Fatalf("upgrade --restart exit %d stderr=%q", code, errOut.String())
	}
	if execPath != bin {
		t.Fatalf("exec path = %q, want %q", execPath, bin)
	}
}

func TestResolveUpgradeBinPathFollowsSymlink(t *testing.T) {
	// Create a fake binary in a "real" directory.
	realDir := t.TempDir()
	realBin := filepath.Join(realDir, "zarlcode")
	if err := os.WriteFile(realBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink to the fake binary.
	linkDir := t.TempDir()
	linkBin := filepath.Join(linkDir, "zarlcode")
	if err := os.Symlink(realBin, linkBin); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	// Validate that a symlink path is accepted by validateUpgradeBinPath.
	if err := validateUpgradeBinPath(linkBin); err != nil {
		t.Fatalf("validateUpgradeBinPath(%q) = %v", linkBin, err)
	}

	// Ensure that the symlink resolves to the real path.
	resolved, err := filepath.EvalSymlinks(linkBin)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) = %v", linkBin, err)
	}
	if resolved != realBin {
		t.Fatalf("EvalSymlinks(%q) = %q, want %q", linkBin, resolved, realBin)
	}
}
