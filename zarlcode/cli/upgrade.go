package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/version"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// defaultUpgradeRepo is the GitHub repository zarlcode pulls release binaries
// from when no source override is configured.
const defaultUpgradeRepo = "zarldev/zarlmono"

const (
	settingKeyUpgradeSource  = prefs.KeyUpgradeSource
	settingKeyUpgradeRestart = prefs.KeyUpgradeRestart
	settingKeyUpgradeDryRun  = prefs.KeyUpgradeDryRun
	settingKeyUpgradeBinPath = prefs.KeyUpgradeBinPath
)

type upgradeOptions struct {
	Version         string
	DryRun          bool
	DryRunOverride  bool
	Restart         bool
	RestartOverride bool
	Stdout          io.Writer
	Stderr          io.Writer
}

type upgradeResult struct {
	Repo      string
	Version   string
	AssetName string
	AssetURL  string
	BinPath   string
	DryRun    bool
	Restart   bool
	UpToDate  bool
}

type upgradeExecFunc func(path string, argv, env []string) error

var execUpgradeBinary upgradeExecFunc = syscall.Exec

// repoSlugRE matches a GitHub "owner/repo" slug. The upgrade source setting
// holds this rather than a local checkout — releases are the upgrade channel.
var repoSlugRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func RunUpgrade(args []string, stdout io.Writer) int {
	ctx := context.Background()
	if _, err := home.Materialise(); err != nil {
		fmt.Fprintln(os.Stderr, "init home:", err)
		return 1
	}
	store, err := db.Open(ctx, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "store:", err)
		return 1
	}
	defer store.Close()
	svc := prefs.NewService(store, nil, "")
	return runUpgradeWithService(ctx, svc, args, stdout, os.Stderr, true)
}

func runUpgradeWithService(ctx context.Context, svc *prefs.Service, args []string, stdout, stderr io.Writer, allowExec bool) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			printUpgradeHelp(stdout)
			return 0
		case cmdStatus:
			return upgradeStatus(ctx, svc, stdout, stderr)
		case flagSource:
			return upgradeValueSetting(ctx, svc, args[1:], settingKeyUpgradeSource, flagSource, stdout, stderr, normalizeUpgradeSource)
		case "bin-path":
			return upgradeValueSetting(ctx, svc, args[1:], settingKeyUpgradeBinPath, "bin-path", stdout, stderr, normalizeUpgradeBinPath)
		case flagRestart:
			return upgradeBoolSetting(ctx, svc, args[1:], settingKeyUpgradeRestart, flagRestart, stdout, stderr)
		case flagDryRun:
			return upgradeBoolSetting(ctx, svc, args[1:], settingKeyUpgradeDryRun, flagDryRun, stdout, stderr)
		}
	}

	opts, err := parseUpgradeRunArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	opts.Stdout = stdout
	opts.Stderr = stderr
	res, err := runUpgrade(ctx, svc, opts)
	if err != nil {
		fmt.Fprintln(stderr, "upgrade:", err)
		return 1
	}
	if res.DryRun {
		fmt.Fprintf(stdout, "repo: %s\n", res.Repo)
		fmt.Fprintf(stdout, "version: %s\n", res.Version)
		fmt.Fprintf(stdout, "asset: %s\n", res.AssetName)
		fmt.Fprintf(stdout, "binary: %s\n", res.BinPath)
		return 0
	}
	if res.UpToDate {
		fmt.Fprintf(stdout, "already up to date: %s (%s)\n", res.Version, res.BinPath)
		return 0
	}
	fmt.Fprintf(stdout, "upgrade complete: installed %s to %s\n", res.Version, res.BinPath)
	if res.Restart {
		fmt.Fprintf(stdout, "restarting: %s\n", res.BinPath)
		if allowExec {
			if err := execUpgradeBinary(res.BinPath, []string{res.BinPath}, os.Environ()); err != nil {
				fmt.Fprintln(stderr, "restart:", err)
				return 1
			}
		}
	} else {
		fmt.Fprintln(stdout, "restart zarlcode to use the new binary")
	}
	return 0
}

func parseUpgradeRunArgs(args []string) (upgradeOptions, error) {
	var opts upgradeOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--dry-run":
			opts.DryRun = true
			opts.DryRunOverride = true
		case "--restart":
			opts.Restart = true
			opts.RestartOverride = true
		case "--no-restart":
			opts.Restart = false
			opts.RestartOverride = true
		case "--version":
			if i+1 >= len(args) {
				return opts, errors.New("--version needs a value")
			}
			i++
			opts.Version = strings.TrimSpace(args[i])
		default:
			if v, ok := strings.CutPrefix(arg, "--version="); ok {
				opts.Version = strings.TrimSpace(v)
				continue
			}
			return opts, fmt.Errorf("unknown upgrade argument %q", arg)
		}
	}
	return opts, nil
}

func runUpgrade(ctx context.Context, svc *prefs.Service, opts upgradeOptions) (upgradeResult, error) {
	repo, err := resolveUpgradeRepo(ctx, svc)
	if err != nil {
		return upgradeResult{}, err
	}
	binPath, err := resolveUpgradeBinPath(ctx, svc)
	if err != nil {
		return upgradeResult{}, err
	}
	if !opts.DryRunOverride {
		opts.DryRun = boolSetting(ctx, svc, settingKeyUpgradeDryRun, false)
	}
	if !opts.RestartOverride {
		opts.Restart = boolSetting(ctx, svc, settingKeyUpgradeRestart, false)
	}

	goos, goarch := currentGOOS(), currentGOARCH()
	rel, archive, checksums, err := resolveRelease(ctx, repo, opts.Version, goos, goarch)
	if err != nil {
		return upgradeResult{}, err
	}
	res := upgradeResult{
		Repo:      repo,
		Version:   releaseVersion(rel.TagName),
		AssetName: archive.Name,
		AssetURL:  archive.URL,
		BinPath:   binPath,
		DryRun:    opts.DryRun,
		Restart:   opts.Restart,
	}
	if opts.DryRun {
		return res, nil
	}
	// Skip the download when the running build already matches the resolved
	// release and the caller didn't pin a specific version.
	if (opts.Version == "" || opts.Version == "latest") && res.Version == version.String() {
		res.UpToDate = true
		return res, nil
	}

	archiveData, err := downloadBytes(ctx, archive.URL)
	if err != nil {
		return res, err
	}
	manifest, err := downloadBytes(ctx, checksums.URL)
	if err != nil {
		return res, err
	}
	if err := verifyChecksum(archive.Name, archiveData, manifest); err != nil {
		return res, err
	}
	binData, err := extractBinary(archive.Name, archiveData, goos)
	if err != nil {
		return res, err
	}
	if err := installBinary(binPath, binData, goos); err != nil {
		return res, err
	}
	return res, nil
}

func resolveUpgradeRepo(ctx context.Context, svc *prefs.Service) (string, error) {
	if v, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource); err != nil {
		return "", err
	} else if ok && strings.TrimSpace(v.Value) != "" {
		repo := strings.TrimSpace(v.Value)
		if err := validateUpgradeSource(repo); err != nil {
			return "", fmt.Errorf("configured source %q: %w", repo, err)
		}
		return repo, nil
	}
	return defaultUpgradeRepo, nil
}

func resolveUpgradeBinPath(ctx context.Context, svc *prefs.Service) (string, error) {
	if v, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath); err != nil {
		return "", err
	} else if ok && strings.TrimSpace(v.Value) != "" {
		path := strings.TrimSpace(v.Value)
		if err := validateUpgradeBinPath(path); err != nil {
			return "", fmt.Errorf("configured binary path %q: %w", path, err)
		}
		return path, nil
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		resolved, err := filepath.EvalSymlinks(exe)
		if err != nil {
			resolved = exe
		}
		if abs, err := filepath.Abs(resolved); err == nil && validateUpgradeBinPath(abs) == nil {
			_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, abs)
			return abs, nil
		}
	}
	home, err := home.SafeUserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot resolve home directory for default binary path")
	}
	path := filepath.Join(home, ".local", "bin", "zarlcode")
	if err := validateUpgradeBinPath(path); err != nil {
		return "", err
	}
	_ = svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeBinPath, path)
	return path, nil
}

// validateUpgradeSource checks that the configured source is a GitHub
// "owner/repo" slug — the release channel zarlcode pulls binaries from.
func validateUpgradeSource(repo string) error {
	if strings.TrimSpace(repo) == "" {
		return errors.New("empty repository")
	}
	if !repoSlugRE.MatchString(strings.TrimSpace(repo)) {
		return errors.New(`want a GitHub repository slug "owner/repo"`)
	}
	return nil
}

// normalizeUpgradeSource canonicalizes a repo slug, also accepting a full
// github.com URL for convenience.
func normalizeUpgradeSource(raw string) (string, error) {
	repo := strings.TrimSpace(raw)
	repo = strings.TrimPrefix(repo, "https://github.com/")
	repo = strings.TrimPrefix(repo, "github.com/")
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.Trim(repo, "/")
	if err := validateUpgradeSource(repo); err != nil {
		return "", err
	}
	return repo, nil
}

// normalizeUpgradeBinPath resolves the install path to an absolute path and
// validates its parent directory exists.
func normalizeUpgradeBinPath(raw string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if err := validateUpgradeBinPath(abs); err != nil {
		return "", err
	}
	return abs, nil
}

func validateUpgradeBinPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty binary path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	st, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("binary parent directory: %w", err)
	}
	if !st.IsDir() {
		return errors.New("binary parent is not a directory")
	}
	return nil
}

func upgradeValueSetting(ctx context.Context, svc *prefs.Service, args []string, key, label string, stdout, stderr io.Writer, normalize func(string) (string, error)) int {
	if len(args) == 0 || args[0] == "show" {
		v, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, key)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !ok || v.Value == "" {
			fmt.Fprintf(stdout, "%s: (unset)\n", label)
			return 0
		}
		fmt.Fprintf(stdout, "%s: %s\n", label, v.Value)
		return 0
	}
	switch args[0] {
	case subcmdSet:
		if len(args) < 2 {
			fmt.Fprintf(stderr, "usage: zarlcode upgrade %s set <value>\n", label)
			return 2
		}
		value, err := normalize(strings.Join(args[1:], " "))
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", label, err)
			return 1
		}
		if err := svc.SetSetting(ctx, prefs.ScopeGlobal, key, value); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s\n", label, value)
		return 0
	case subcmdClear, subcmdDelete, "rm":
		if err := svc.DeleteSetting(ctx, prefs.ScopeGlobal, key); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: cleared\n", label)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown %s command %q\n", label, args[0])
		return 2
	}
}

func upgradeBoolSetting(ctx context.Context, svc *prefs.Service, args []string, key, label string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "show" {
		fmt.Fprintf(stdout, "%s: %v\n", label, boolSetting(ctx, svc, key, false))
		return 0
	}
	switch args[0] {
	case subcmdSet:
		if len(args) != 2 {
			fmt.Fprintf(stderr, "usage: zarlcode upgrade %s set <true|false>\n", label)
			return 2
		}
		b, err := strconv.ParseBool(args[1])
		if err != nil {
			fmt.Fprintf(stderr, "%s: want true or false\n", label)
			return 2
		}
		if err := svc.SetSetting(ctx, prefs.ScopeGlobal, key, strconv.FormatBool(b)); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %v\n", label, b)
		return 0
	case subcmdClear, subcmdDelete, "rm":
		if err := svc.DeleteSetting(ctx, prefs.ScopeGlobal, key); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: cleared\n", label)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown %s command %q\n", label, args[0])
		return 2
	}
}

func boolSetting(ctx context.Context, svc *prefs.Service, key string, fallback bool) bool {
	v, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, key)
	if err != nil || !ok || strings.TrimSpace(v.Value) == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v.Value)
	if err != nil {
		return fallback
	}
	return b
}

func upgradeStatus(ctx context.Context, svc *prefs.Service, stdout, stderr io.Writer) int {
	for _, row := range []struct {
		label string
		key   string
	}{
		{flagSource, settingKeyUpgradeSource},
		{"binary", settingKeyUpgradeBinPath},
		{flagRestart, settingKeyUpgradeRestart},
		{flagDryRun, settingKeyUpgradeDryRun},
	} {
		v, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, row.key)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !ok || v.Value == "" {
			// source defaults to the canonical release repo when unset.
			if row.key == settingKeyUpgradeSource {
				fmt.Fprintf(stdout, "%s: %s (default)\n", row.label, defaultUpgradeRepo)
				continue
			}
			fmt.Fprintf(stdout, "%s: (unset)\n", row.label)
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", row.label, v.Value)
	}
	return 0
}

func printUpgradeHelp(w io.Writer) {
	fmt.Fprintln(w, `usage: zarlcode upgrade [--dry-run] [--restart|--no-restart] [--version vX.Y.Z]
       zarlcode upgrade status
       zarlcode upgrade source set <owner/repo>
       zarlcode upgrade source clear
       zarlcode upgrade bin-path set <path>
       zarlcode upgrade bin-path clear
       zarlcode upgrade restart set <true|false>
       zarlcode upgrade dry-run set <true|false>

Downloads the published zarlcode release binary for this OS/arch from the
GitHub `+"`source`"+` repository (default `+defaultUpgradeRepo+`), verifies its sha256
against the release checksums, and installs it to `+"`bin-path`"+`. Without
--version the latest release is used. Upgrade preferences are stored globally
in zarlcode's SQLite settings.`)
}
