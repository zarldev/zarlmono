// doctor is the preflight check for new contributors. It catches
// the predictable missed-step failures (missing toolchain, .env not
// copied, model files not extracted, sidecar services down) before
// zarl tries to start and dumps a confusing error.
//
// Run via `task doctor`. Exits 0 if every required check passes,
// 1 if any fails. Yellow warnings (optional checks) don't fail.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	minGoVersion   = "1.26"
	minNodeVersion = "20"
	probeTimeout   = 3 * time.Second
)

func main() {
	c := newChecker(os.Stdout)

	c.section("Toolchain")
	c.checkVersionedTool("go", minGoVersion, []string{"version"}, parseGoVersion, "install from go.dev")
	c.checkVersionedTool("node", minNodeVersion, []string{"-v"}, parseNodeVersion, "install from nodejs.org")
	c.checkPresent("task", "task --version", "install from taskfile.dev")
	c.checkPresent("buf", "buf --version", "install from buf.build/docs/installation")
	c.checkDocker()

	c.section("Configuration")
	repoRoot := detectRepoRoot()
	envPath := filepath.Join(repoRoot, ".env")
	envFile, _ := loadEnvFile(envPath)
	if envFile != nil {
		c.ok(".env present")
	} else {
		c.fail(".env missing", `cp .env.example .env && $EDITOR .env`)
	}
	getEnv := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return envFile[k]
	}
	c.checkRequiredVar(getEnv, "CHAT_URL")
	c.checkRequiredVar(getEnv, "CHAT_MODEL")

	c.section("Models")
	modelsDir := getEnv("MODELS_DIR")
	if modelsDir == "" {
		modelsDir = filepath.Join(repoRoot, "deploy", "models")
	}
	c.checkSTT(filepath.Join(modelsDir, "whisper-small-en"))
	c.checkTTS(filepath.Join(modelsDir, "kokoro-en-v0_19"))
	c.checkFace(filepath.Join(modelsDir, "dlib"))
	c.checkGGUF(modelsDir)

	c.section("Services")
	if u := getEnv("CHAT_URL"); u != "" {
		c.probeURL("llama-server", strings.TrimSuffix(u, "/")+"/models", "task up:llm or point CHAT_URL elsewhere")
	}
	ollamaBase := getEnv("EMBED_URL")
	if ollamaBase == "" {
		ollamaBase = "http://localhost:11434"
	}
	c.probeURL("Ollama", strings.TrimSuffix(ollamaBase, "/")+"/api/tags", "ollama serve")
	c.probeURL("Qdrant", "http://localhost:6333/healthz", "task up")
	c.probeURL("SearXNG", "http://localhost:8888/", "task up")
	c.checkDoltCompose()

	c.section("Result")
	c.summary()
	if c.failed > 0 {
		os.Exit(1)
	}
}

// --- check engine ---

type checker struct {
	out                    *os.File
	color                  bool
	passed, warned, failed int
}

func newChecker(out *os.File) *checker {
	fi, _ := out.Stat()
	return &checker{
		out:   out,
		color: fi != nil && (fi.Mode()&os.ModeCharDevice != 0),
	}
}

const (
	colReset  = "\033[0m"
	colBold   = "\033[1m"
	colGreen  = "\033[32m"
	colRed    = "\033[31m"
	colYellow = "\033[33m"
)

func (c *checker) col(code string) string {
	if !c.color {
		return ""
	}
	return code
}

func (c *checker) section(name string) {
	fmt.Fprintf(c.out, "\n%s%s%s\n", c.col(colBold), name, c.col(colReset))
}

func (c *checker) ok(msg string) {
	fmt.Fprintf(c.out, "  %s✓%s %s\n", c.col(colGreen), c.col(colReset), msg)
	c.passed++
}

func (c *checker) warn(msg, hint string) {
	fmt.Fprintf(c.out, "  %s!%s %s — %s%s%s\n",
		c.col(colYellow), c.col(colReset),
		msg, c.col(colYellow), hint, c.col(colReset))
	c.warned++
}

func (c *checker) fail(msg, hint string) {
	fmt.Fprintf(c.out, "  %s✗%s %s — %s%s%s\n",
		c.col(colRed), c.col(colReset),
		msg, c.col(colYellow), hint, c.col(colReset))
	c.failed++
}

func (c *checker) summary() {
	fmt.Fprintf(c.out, "  passed: %s%d%s    failed: %s%d%s    warnings: %s%d%s\n",
		c.col(colGreen), c.passed, c.col(colReset),
		c.col(colRed), c.failed, c.col(colReset),
		c.col(colYellow), c.warned, c.col(colReset))
}

// --- toolchain checks ---

// checkVersionedTool runs `<bin> <args...>`, parses the version with
// parse, and compares against minVer.
func (c *checker) checkVersionedTool(bin, minVer string, args []string, parse func(string) string, hint string) {
	if _, err := exec.LookPath(bin); err != nil {
		c.fail(bin+" not on PATH", hint)
		return
	}
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		c.fail(bin+" failed: "+err.Error(), hint)
		return
	}
	v := parse(string(out))
	if v == "" {
		c.fail(bin+" version unparseable", hint)
		return
	}
	if !versionGTE(v, minVer) {
		c.fail(fmt.Sprintf("%s %s", bin, v), fmt.Sprintf("need ≥ %s — %s", minVer, hint))
		return
	}
	c.ok(fmt.Sprintf("%s %s (≥ %s)", bin, v, minVer))
}

func (c *checker) checkPresent(bin, versionCmd, hint string) {
	if _, err := exec.LookPath(bin); err != nil {
		c.fail(bin+" not on PATH", hint)
		return
	}
	parts := strings.Fields(versionCmd)
	out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
	if err != nil {
		c.ok(bin + " present")
		return
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	c.ok(bin + " " + first)
}

func (c *checker) checkDocker() {
	if _, err := exec.LookPath("docker"); err != nil {
		c.fail("docker not on PATH", "install Docker Desktop or docker-ce")
		return
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		c.fail("docker installed but daemon unreachable", "start Docker Desktop / dockerd")
		return
	}
	c.ok("docker daemon reachable")
}

// --- env / config ---

func (c *checker) checkRequiredVar(get func(string) string, name string) {
	if get(name) != "" {
		c.ok(name + " set")
		return
	}
	c.fail(name+" unset", "see .env.example")
}

// --- models ---

func (c *checker) checkSTT(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.onnx"))
	if len(matches) == 0 {
		c.fail("no .onnx in "+dir, "see Models section in README")
		return
	}
	c.ok("STT models present (" + dir + ")")
}

func (c *checker) checkTTS(dir string) {
	model := filepath.Join(dir, "model.onnx")
	if !fileExists(model) {
		c.fail(model+" missing", "see Models section in README")
		return
	}
	c.ok("TTS model present (" + model + ")")
}

func (c *checker) checkFace(dir string) {
	required := []string{
		"shape_predictor_5_face_landmarks.dat",
		"dlib_face_recognition_resnet_model_v1.dat",
	}
	for _, f := range required {
		if !fileExists(filepath.Join(dir, f)) {
			c.fail("dlib models incomplete", "shape_predictor_5_face_landmarks.dat + dlib_face_recognition_resnet_model_v1.dat — dlib.net/files")
			return
		}
	}
	c.ok("dlib models present (" + dir + ")")
}

func (c *checker) checkGGUF(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.gguf"))
	if len(matches) == 0 {
		c.warn("no .gguf in "+dir, "ok if using a hosted endpoint or Ollama; otherwise download from huggingface.co/unsloth")
		return
	}
	c.ok("GGUF weights present (" + dir + ")")
}

// --- services ---

func (c *checker) probeURL(label, url, hint string) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		c.fail(label+" unreachable ("+url+")", hint)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.fail(label+" unreachable ("+url+")", hint)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		c.fail(fmt.Sprintf("%s returned %d (%s)", label, resp.StatusCode, url), hint)
		return
	}
	c.ok(label + " reachable (" + url + ")")
}

func (c *checker) checkDoltCompose() {
	if _, err := exec.LookPath("docker"); err != nil {
		c.fail("Dolt not running", "task up")
		return
	}
	out, err := exec.Command("docker", "compose", "ps", "--status", "running", "--services").Output()
	if err != nil {
		c.fail("Dolt not running", "task up")
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "dolt" {
			c.ok("Dolt service running")
			return
		}
	}
	c.fail("Dolt not running", "task up")
}

// --- helpers ---

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func detectRepoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, _ := os.Getwd()
	return wd
}

// loadEnvFile reads a KEY=VALUE file (skipping blanks and # comments)
// into a map. Returns nil if the file doesn't exist.
func loadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := make(map[string]string)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		out[k] = v
	}
	return out, s.Err()
}

var goVersionRe = regexp.MustCompile(`go(\d+(?:\.\d+)+)`)

func parseGoVersion(out string) string {
	if m := goVersionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

func parseNodeVersion(out string) string {
	return strings.TrimPrefix(strings.TrimSpace(out), "v")
}

// versionGTE compares dotted-numeric versions. Non-numeric segments
// compare as zero.
func versionGTE(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ai, bi int
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai != bi {
			return ai > bi
		}
	}
	return true
}
