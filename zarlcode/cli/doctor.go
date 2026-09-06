package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/version"
	"github.com/zarldev/zarlmono/zkit/db"
)

const (
	doctorOK     = "OK"
	doctorWarn   = "WARN"
	doctorFail   = "FAIL"
	vaultKDFFile = "master.kdf"
)

// RunDoctor validates arguments, then performs offline, read-only checks of the
// running binary and local zarlcode state. Missing optional or first-run state
// produces warnings; invalid or unreadable existing state produces a non-zero exit
// status. Usage errors return 4 without running checks.
func RunDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
			printDoctorHelp(stdout)
			return 0
		}
		fmt.Fprintln(stderr, "doctor: no arguments accepted")
		printDoctorHelp(stderr)
		return 4
	}

	report := doctorReport{output: stdout}
	fmt.Fprintln(stdout, "zarlcode doctor")
	fmt.Fprintln(stdout, "mode: offline, read-only")

	report.checkRuntime()
	homeDir, err := home.HomeDir()
	if err != nil {
		report.add(doctorFail, "home", err.Error())
		report.printSummary()
		return 1
	}

	exists, usable := report.checkHome(homeDir)
	if usable {
		if exists {
			report.checkExtensions(homeDir)
			report.checkState()
			report.checkVault(homeDir)
		} else {
			report.add(doctorOK, "state database", "not initialized")
			report.add(doctorOK, "credential vault", "not configured")
		}
	}

	report.printSummary()
	if report.failures > 0 {
		return 1
	}
	return 0
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: zarlcode doctor")
	fmt.Fprintln(w, "Run offline, read-only checks of the local zarlcode installation.")
}

type doctorReport struct {
	output   io.Writer
	oks      int
	warnings int
	failures int
}

func (r *doctorReport) add(status, label, detail string) {
	switch status {
	case doctorOK:
		r.oks++
	case doctorWarn:
		r.warnings++
	case doctorFail:
		r.failures++
	}
	fmt.Fprintf(r.output, "%-5s %s: %s\n", status, label, detail)
}

func (r *doctorReport) printSummary() {
	warningLabel := "warnings"
	if r.warnings == 1 {
		warningLabel = "warning"
	}
	failureLabel := "failures"
	if r.failures == 1 {
		failureLabel = "failure"
	}
	fmt.Fprintf(r.output, "summary: %d ok, %d %s, %d %s\n", r.oks, r.warnings, warningLabel, r.failures, failureLabel)
}

func (r *doctorReport) checkRuntime() {
	r.add(doctorOK, "version", version.String())

	executable, err := os.Executable()
	if err != nil {
		r.add(doctorFail, "executable", err.Error())
		return
	}
	executable = filepath.Clean(executable)
	info, err := os.Stat(executable)
	if err != nil {
		r.add(doctorFail, "executable", err.Error())
		return
	}
	if !info.Mode().IsRegular() {
		r.add(doctorFail, "executable", executable+" is not a regular file")
		return
	}
	r.add(doctorOK, "executable", executable)
}

func (r *doctorReport) checkHome(path string) (bool, bool) {
	info, exists, err := inspectPath(path)
	switch {
	case err != nil:
		r.add(doctorFail, "home", err.Error())
		return exists, false
	case !exists:
		r.add(doctorWarn, "home", fmt.Sprintf("not initialized at %s; run `zarlcode init`", path))
		return false, true
	case !info.IsDir():
		r.add(doctorFail, "home", path+" is not a directory")
		return true, false
	default:
		r.add(doctorOK, "home", path)
		return true, true
	}
}

func (r *doctorReport) checkExtensions(homeDir string) {
	for _, name := range []string{"skills", "tools", "hooks"} {
		path := filepath.Join(homeDir, name)
		info, exists, err := inspectPath(path)
		switch {
		case err != nil:
			r.add(doctorFail, name, err.Error())
		case !exists:
			r.add(doctorWarn, name, "missing; run `zarlcode init`")
		case !info.IsDir():
			r.add(doctorFail, name, path+" is not a directory")
		default:
			r.add(doctorOK, name, path)
		}
	}
}

func (r *doctorReport) checkState() {
	path, err := db.DefaultPath()
	if err != nil {
		r.add(doctorFail, "state database", err.Error())
		return
	}
	exists, err := readableRegularFile(path)
	switch {
	case err != nil:
		r.add(doctorFail, "state database", err.Error())
	case !exists:
		r.add(doctorOK, "state database", "not initialized")
	default:
		r.add(doctorOK, "state database", path+" is present and readable")
	}
}

func (r *doctorReport) checkVault(homeDir string) {
	exists, err := readableRegularFile(filepath.Join(homeDir, vaultKDFFile))
	switch {
	case err != nil:
		r.add(doctorFail, "credential vault", err.Error())
	case !exists:
		r.add(doctorOK, "credential vault", "not configured")
	default:
		r.add(doctorOK, "credential vault", "passphrase material is present and readable")
	}
}

func readableRegularFile(path string) (bool, error) {
	info, exists, err := inspectPath(path)
	if err != nil || !exists {
		return exists, err
	}
	if !info.Mode().IsRegular() {
		return true, fmt.Errorf("%s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return true, err
	}
	return true, file.Close()
}

func inspectPath(path string) (fs.FileInfo, bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(path)
	return info, true, err
}
