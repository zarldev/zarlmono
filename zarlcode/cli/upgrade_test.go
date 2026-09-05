package cli_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/cli"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

func archive(t *testing.T, tag, goos, arch string) (string, []byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("fake-zarlcode-" + tag + "-" + goos + "-" + arch)
	bin := "zarlcode"
	if goos == "windows" {
		bin += ".exe"
	}
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
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	return fmt.Sprintf("zarlcode_%s_%s_%s.tar.gz", tag, goos, arch), data, hex.EncodeToString(sum[:])
}

func server(t *testing.T, tag string, assets map[string][]byte, seen *string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var aa []map[string]string
		for name := range assets {
			aa = append(aa, map[string]string{"name": name, "browser_download_url": "http://" + r.Host + "/dl/" + name})
		}
		release := map[string]any{"tag_name": tag, "assets": aa}
		switch {
		case strings.Contains(r.URL.EscapedPath(), "/releases/tags/"):
			got := strings.TrimPrefix(r.URL.EscapedPath(), "/repos/acme/tool/releases/tags/")
			if seen != nil {
				*seen = got
			}
			if got != url.PathEscape(tag) {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(release)
		case strings.HasSuffix(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]any{release})
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
	t.Cleanup(s.Close)
	return s.URL
}

func command(t *testing.T, arch string) (cli.UpgradeCommand, *prefs.Service) {
	t.Helper()
	svc := prefs.NewService(openTestStore(t), nil, "")
	return cli.UpgradeCommand{Service: svc, GOOS: "linux", GOARCH: arch}, svc
}

func execute(t *testing.T, cmd cli.UpgradeCommand, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := cmd.Execute(t.Context(), args, &out, &stderr, false)
	return code, out.String(), stderr.String()
}

func configure(t *testing.T, cmd cli.UpgradeCommand, bin string) {
	t.Helper()
	for _, args := range [][]string{{"source", "set", "acme/tool"}, {"bin-path", "set", bin}} {
		if code, _, stderr := execute(t, cmd, args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, stderr)
		}
	}
}

func TestUpgradeSourceValidationAndNormalization(t *testing.T) {
	cmd, _ := command(t, "amd64")
	for _, value := range []string{"zarldev/zarlmono", "acme/tool-1", "a.b/c_d"} {
		if code, _, stderr := execute(t, cmd, "source", "set", value); code != 0 {
			t.Errorf("%q: %s", value, stderr)
		}
	}
	for _, value := range []string{"", "noslash", "owner/repo/extra", "owner /repo"} {
		if code, _, _ := execute(t, cmd, "source", "set", value); code == 0 {
			t.Errorf("%q accepted", value)
		}
	}
	for input, want := range map[string]string{"https://github.com/zarldev/zarlmono": "zarldev/zarlmono", "https://github.com/zarldev/zarlmono.git": "zarldev/zarlmono", "github.com/acme/tool/": "acme/tool"} {
		code, out, stderr := execute(t, cmd, "source", "set", input)
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%q: exit %d out=%q err=%q", input, code, out, stderr)
		}
	}
}

func TestUpgradeSettingsPersistGlobally(t *testing.T) {
	cmd, svc := command(t, "amd64")
	bin := filepath.Join(t.TempDir(), "zarlcode")
	cases := []struct {
		args      []string
		key, want string
	}{
		{[]string{"source", "set", "acme/tool"}, prefs.KeyUpgradeSource, "acme/tool"},
		{[]string{"bin-path", "set", bin}, prefs.KeyUpgradeBinPath, bin},
		{[]string{"restart", "set", "true"}, prefs.KeyUpgradeRestart, "true"},
		{[]string{"dry-run", "set", "true"}, prefs.KeyUpgradeDryRun, "true"},
	}
	for _, tc := range cases {
		if code, _, stderr := execute(t, cmd, tc.args...); code != 0 {
			t.Fatalf("%v: %s", tc.args, stderr)
		}
		got, err := svc.GetSetting(t.Context(), prefs.ScopeGlobal, tc.key)
		if err != nil || got.Value != tc.want {
			t.Fatalf("%s = %+v, %v", tc.key, got, err)
		}
	}
	if code, _, stderr := execute(t, cmd, "source", "clear"); code != 0 {
		t.Fatal(stderr)
	}
	if _, err := svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyUpgradeSource); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("source remains: %v", err)
	}
}

func TestUpgradeMigratesLegacySource(t *testing.T) {
	cmd, svc := command(t, "amd64")
	if err := svc.SetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyUpgradeSource, filepath.Join(t.TempDir(), "zarlmono")); err != nil {
		t.Fatal(err)
	}
	name, data, sum := archive(t, "v1.2.3", "linux", "amd64")
	cmd.APIBase = server(t, "zarlcode/v1.2.3", map[string][]byte{name: data, "checksums.txt": []byte(sum + "  " + name + "\n")}, nil)
	if code, _, stderr := execute(t, cmd, "bin-path", "set", filepath.Join(t.TempDir(), "zarlcode")); code != 0 {
		t.Fatal(stderr)
	}
	code, out, stderr := execute(t, cmd, "--dry-run")
	if code != 0 || !strings.Contains(out, "repo: zarldev/zarlmono") {
		t.Fatalf("exit %d out=%q err=%q", code, out, stderr)
	}
	if _, err := svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeyUpgradeSource); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("legacy source remains: %v", err)
	}
}

func TestUpgradeDryRunDoesNotDownload(t *testing.T) {
	cmd, _ := command(t, "amd64")
	name, data, sum := archive(t, "v1.2.3", "linux", "amd64")
	cmd.APIBase = server(t, "zarlcode/v1.2.3", map[string][]byte{name: data, "checksums.txt": []byte(sum + "  " + name + "\n")}, nil)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	configure(t, cmd, bin)
	code, out, stderr := execute(t, cmd, "--dry-run")
	if code != 0 || !strings.Contains(out, "version: v1.2.3") || !strings.Contains(out, "asset: "+name) {
		t.Fatalf("exit %d out=%q err=%q", code, out, stderr)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("binary written: %v", err)
	}
}

func TestUpgradeInstallsReleaseBinary(t *testing.T) {
	cmd, _ := command(t, "amd64")
	name, data, sum := archive(t, "v2.0.0", "linux", "amd64")
	other, otherData, otherSum := archive(t, "v2.0.0", "darwin", "arm64")
	cmd.APIBase = server(t, "zarlcode/v2.0.0", map[string][]byte{name: data, other: otherData, "checksums.txt": []byte(sum + "  " + name + "\n" + otherSum + "  " + other + "\n")}, nil)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	configure(t, cmd, bin)
	if code, _, stderr := execute(t, cmd); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got, err := os.ReadFile(bin)
	if err != nil || string(got) != "fake-zarlcode-v2.0.0-linux-amd64" {
		t.Fatalf("binary=%q err=%v", got, err)
	}
	info, err := os.Stat(bin)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
}

func TestUpgradeRejectsChecksumMismatch(t *testing.T) {
	cmd, _ := command(t, "amd64")
	name, data, _ := archive(t, "v1.0.0", "linux", "amd64")
	cmd.APIBase = server(t, "zarlcode/v1.0.0", map[string][]byte{name: data, "checksums.txt": []byte(strings.Repeat("0", 64) + "  " + name + "\n")}, nil)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	configure(t, cmd, bin)
	code, _, stderr := execute(t, cmd)
	if code == 0 || !strings.Contains(stderr, "checksum mismatch") {
		t.Fatalf("exit %d err=%q", code, stderr)
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("binary written: %v", err)
	}
}

func TestUpgradeErrorsWithoutPlatformAsset(t *testing.T) {
	cmd, _ := command(t, "arm64")
	name, data, sum := archive(t, "v1.0.0", "linux", "amd64")
	cmd.APIBase = server(t, "zarlcode/v1.0.0", map[string][]byte{name: data, "checksums.txt": []byte(sum + "  " + name + "\n")}, nil)
	configure(t, cmd, filepath.Join(t.TempDir(), "zarlcode"))
	code, _, stderr := execute(t, cmd)
	if code == 0 || !strings.Contains(stderr, "no installable acme/tool release for linux/arm64") {
		t.Fatalf("exit %d err=%q", code, stderr)
	}
}

func TestUpgradeInstallsPinnedVersion(t *testing.T) {
	cmd, _ := command(t, "amd64")
	name, data, sum := archive(t, "v1.5.0", "linux", "amd64")
	var seen string
	cmd.APIBase = server(t, "zarlcode/v1.5.0", map[string][]byte{name: data, "checksums.txt": []byte(sum + "  " + name + "\n")}, &seen)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	configure(t, cmd, bin)
	if code, _, stderr := execute(t, cmd, "--version", "v1.5.0"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if seen != url.PathEscape("zarlcode/v1.5.0") {
		t.Fatalf("tag path=%q", seen)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeRestartExecsInstalledBinary(t *testing.T) {
	cmd, _ := command(t, "amd64")
	name, data, sum := archive(t, "v3.0.0", "linux", "amd64")
	cmd.APIBase = server(t, "zarlcode/v3.0.0", map[string][]byte{name: data, "checksums.txt": []byte(sum + "  " + name + "\n")}, nil)
	bin := filepath.Join(t.TempDir(), "zarlcode")
	configure(t, cmd, bin)
	var got string
	cmd.Exec = func(path string, _, _ []string) error { got = path; return nil }
	var out, stderr bytes.Buffer
	if code := cmd.Execute(t.Context(), []string{"--restart"}, &out, &stderr, true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got != bin {
		t.Fatalf("exec=%q want=%q", got, bin)
	}
}

func TestUpgradeBinPathAcceptsSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "zarlcode")
	if err := os.WriteFile(target, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "zarlcode")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	cmd, _ := command(t, "amd64")
	if code, _, stderr := execute(t, cmd, "bin-path", "set", link); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || resolved != target {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}
