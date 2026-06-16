package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

const (
	settingKeyUpgradeSource  = prefs.KeyUpgradeSource
	settingKeyUpgradeRestart = prefs.KeyUpgradeRestart
	settingKeyUpgradeDryRun  = prefs.KeyUpgradeDryRun
	settingKeyUpgradeBinPath = prefs.KeyUpgradeBinPath
)

type upgradeOptions struct {
	DryRun          bool
	DryRunOverride  bool
	Restart         bool
	RestartOverride bool
	Stdout          io.Writer
	Stderr          io.Writer
}

type upgradeResult struct {
	Source  string
	BinPath string
	Command []string
	Output  string
	DryRun  bool
	Restart bool
}

type upgradeCommandRunner func(ctx context.Context, dir string, stdout, stderr io.Writer) (string, error)
type upgradeExecFunc func(path string, argv, env []string) error

var (
	runUpgradeCommand upgradeCommandRunner = runTaskZarlcode
	execUpgradeBinary upgradeExecFunc      = syscall.Exec
)

var taskZarlcodeTargetRE = regexp.MustCompile(`(?m)^[ \t]+['"]?zarlcode['"]?\s*:`)

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
			return upgradePathSetting(ctx, svc, args[1:], settingKeyUpgradeSource, flagSource, stdout, stderr, validateUpgradeSource)
		case "bin-path":
			return upgradePathSetting(ctx, svc, args[1:], settingKeyUpgradeBinPath, "bin-path", stdout, stderr, validateUpgradeBinPath)
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
		fmt.Fprintf(stdout, "source: %s\n", res.Source)
		fmt.Fprintf(stdout, "binary: %s\n", res.BinPath)
		fmt.Fprintf(stdout, "command: %s\n", strings.Join(res.Command, " "))
		return 0
	}
	fmt.Fprintf(stdout, "upgrade complete: installed %s\n", res.BinPath)
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
	for _, arg := range args {
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
		default:
			return opts, fmt.Errorf("unknown upgrade argument %q", arg)
		}
	}
	return opts, nil
}

func runUpgrade(ctx context.Context, svc *prefs.Service, opts upgradeOptions) (upgradeResult, error) {
	source, err := resolveUpgradeSource(ctx, svc)
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
	res := upgradeResult{
		Source:  source,
		BinPath: binPath,
		Command: []string{"go", "tool", "task", "zarlcode"},
		DryRun:  opts.DryRun,
		Restart: opts.Restart,
	}
	if opts.DryRun {
		return res, nil
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	out, err := runUpgradeCommand(ctx, source, stdout, stderr)
	res.Output = out
	if err != nil {
		return res, err
	}
	return res, nil
}

func runTaskZarlcode(ctx context.Context, dir string, stdout, stderr io.Writer) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "tool", "task", "zarlcode")
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(stdout, &buf)
	cmd.Stderr = io.MultiWriter(stderr, &buf)
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func resolveUpgradeSource(ctx context.Context, svc *prefs.Service) (string, error) {
	if v, ok, err := svc.GetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource); err != nil {
		return "", err
	} else if ok && strings.TrimSpace(v.Value) != "" {
		path := strings.TrimSpace(v.Value)
		if err := validateUpgradeSource(path); err != nil {
			return "", fmt.Errorf("configured source %q: %w", path, err)
		}
		return path, nil
	}
	cwd, err := os.Getwd()
	if err == nil && validateUpgradeSource(cwd) == nil {
		abs, _ := filepath.Abs(cwd)
		if err := svc.SetSetting(ctx, prefs.ScopeGlobal, settingKeyUpgradeSource, abs); err != nil {
			return "", fmt.Errorf("save detected upgrade source: %w", err)
		}
		return abs, nil
	}
	return "", errors.New("source checkout not configured; run: zarlcode upgrade source set /path/to/monorepo")
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

func validateUpgradeSource(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("empty source path")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("not a directory")
	}
	taskfile := filepath.Join(abs, "Taskfile.yml")
	data, err := os.ReadFile(taskfile)
	if errors.Is(err, os.ErrNotExist) {
		taskfile = filepath.Join(abs, "Taskfile.yaml")
		data, err = os.ReadFile(taskfile)
	}
	if err != nil {
		return fmt.Errorf("read Taskfile: %w", err)
	}
	if !taskZarlcodeTargetRE.Match(data) {
		return errors.New("no zarlcode task defined in Taskfile")
	}
	if _, err := os.Stat(filepath.Join(abs, "zarlcode", "cmd", "main.go")); err != nil {
		return fmt.Errorf("zarlcode/cmd/main.go: %w", err)
	}
	return nil
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

func upgradePathSetting(ctx context.Context, svc *prefs.Service, args []string, key, label string, stdout, stderr io.Writer, validate func(string) error) int {
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
			fmt.Fprintf(stderr, "usage: zarlcode upgrade %s set <path>\n", label)
			return 2
		}
		path, err := filepath.Abs(strings.Join(args[1:], " "))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := validate(path); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", label, err)
			return 1
		}
		if err := svc.SetSetting(ctx, prefs.ScopeGlobal, key, path); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %s\n", label, path)
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
			fmt.Fprintf(stdout, "%s: (unset)\n", row.label)
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", row.label, v.Value)
	}
	return 0
}

func printUpgradeHelp(w io.Writer) {
	fmt.Fprintln(w, `usage: zarlcode upgrade [--dry-run] [--restart|--no-restart]
       zarlcode upgrade status
       zarlcode upgrade source set <path>
       zarlcode upgrade source clear
       zarlcode upgrade bin-path set <path>
       zarlcode upgrade bin-path clear
       zarlcode upgrade restart set <true|false>
       zarlcode upgrade dry-run set <true|false>

Rebuilds zarlcode by running `+"`go tool task zarlcode`"+` in the configured source checkout.
Upgrade preferences are stored globally in zarlcode's SQLite settings.`)
}
