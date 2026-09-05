package browser_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
)

func TestNewRejectsMissingChrome(t *testing.T) {
	t.Parallel()

	missing := t.TempDir() + "/missing-chrome"
	_, err := browser.New(t.Context(), browser.WithChromePath(missing))
	if err == nil {
		t.Fatal("New returned nil error for missing Chrome")
	}
	if !strings.Contains(err.Error(), "chrome binary not found at configured path") {
		t.Fatalf("New error = %q, want configured-path error", err)
	}
}

func TestNewHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = browser.New(ctx, browser.WithChromePath(executable))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("New error = %v, want context.Canceled", err)
	}
}

func TestZeroSessionCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	session := new(browser.Session)
	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSessionBrowserIntegration(t *testing.T) {
	if os.Getenv("ZKIT_CHROME_INTEGRATION") != "1" {
		t.Skip("set ZKIT_CHROME_INTEGRATION=1 to run the real-browser contract test")
	}

	chromePath := findChromeBinary(t)
	server := newFixtureServer(t)

	session, err := browser.New(t.Context(),
		browser.WithChromePath(chromePath),
		browser.WithHeadless(true),
		browser.WithActionTimeout(5*time.Second),
		browser.WithSettleWait(10*time.Millisecond),
		browser.WithWindowSize(900, 700),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = session.Close()
		}
	})

	fixtureURL := server.URL + "/fixture"
	obs, err := session.Act(t.Context(), computer.ActionRequest{
		Action: computer.Action{Kind: computer.ActionKinds.NAVIGATE, URL: fixtureURL},
		Until:  &computer.Trigger{Kind: computer.TriggerKinds.NAVIGATIONCOMPLETE},
	})
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if obs.Surface.Title != "Browser contract" {
		t.Errorf("navigate title = %q, want Browser contract", obs.Surface.Title)
	}
	if obs.Surface.URL != fixtureURL {
		t.Errorf("navigate URL = %q, want %q", obs.Surface.URL, fixtureURL)
	}
	if !strings.Contains(obs.VisibleText, "Fixture ready") {
		t.Errorf("navigate visible text = %q, want fixture text", obs.VisibleText)
	}

	nameTarget := &computer.TargetRef{Locator: "#name"}
	obs, err = session.Act(t.Context(), computer.ActionRequest{
		When: &computer.Trigger{
			Kind:   computer.TriggerKinds.VISIBLE,
			Target: nameTarget,
		},
		Action: computer.Action{
			Kind:   computer.ActionKinds.FILL,
			Target: nameTarget,
			Value:  "Ada",
		},
		Until: &computer.Trigger{
			Kind:   computer.TriggerKinds.VALUEEQUALS,
			Target: &computer.TargetRef{ID: "name"},
			Value:  "Ada",
		},
	})
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	if obs.FocusedTarget == nil || obs.FocusedTarget.ID != "#name" || obs.FocusedTarget.Value != "Ada" {
		t.Errorf("focused target after fill = %#v", obs.FocusedTarget)
	}

	_, err = session.Act(t.Context(), computer.ActionRequest{
		Action: computer.Action{
			Kind:   computer.ActionKinds.PRESS,
			Target: &computer.TargetRef{ID: "#name"},
			Key:    "x",
		},
		Until: &computer.Trigger{Kind: computer.TriggerKinds.TEXTPRESENT, Text: "key:x"},
	})
	if err != nil {
		t.Fatalf("press: %v", err)
	}

	_, err = session.Act(t.Context(), computer.ActionRequest{
		When: &computer.Trigger{
			Kind:   computer.TriggerKinds.FOCUSED,
			Target: &computer.TargetRef{ID: "name"},
		},
		Action: computer.Action{
			Kind:   computer.ActionKinds.CLICK,
			Target: &computer.TargetRef{Role: "button", Name: "Submit"},
		},
		Until: &computer.Trigger{Kind: computer.TriggerKinds.URLMATCHES, URL: "#done"},
	})
	if err != nil {
		t.Fatalf("click submit: %v", err)
	}

	_, err = session.Act(t.Context(), computer.ActionRequest{
		Action: computer.Action{
			Kind:   computer.ActionKinds.CLICK,
			Target: &computer.TargetRef{Text: "Hide panel"},
		},
		Until: &computer.Trigger{
			Kind:   computer.TriggerKinds.HIDDEN,
			Target: &computer.TargetRef{ID: "panel"},
		},
	})
	if err != nil {
		t.Fatalf("click hide panel: %v", err)
	}

	_, err = session.Act(t.Context(), computer.ActionRequest{
		Action: computer.Action{
			Kind:  computer.ActionKinds.SCROLL,
			Delta: &computer.Point{X: 0, Y: 400},
		},
		Until: &computer.Trigger{Kind: computer.TriggerKinds.SURFACESTABLE},
	})
	if err != nil {
		t.Fatalf("scroll: %v", err)
	}

	obs, err = session.Observe(t.Context(), computer.ObserveRequest{
		IncludeScreenshot: true,
		IncludeTargets:    true,
		IncludeText:       true,
		IncludeRaw:        true,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.Surface.Kind != computer.SurfaceKinds.BROWSER {
		t.Errorf("surface kind = %v, want browser", obs.Surface.Kind)
	}
	if obs.Surface.Title != "Browser contract" || !strings.Contains(obs.Surface.URL, "#done") {
		t.Errorf("surface = %#v, want fixture title and #done URL", obs.Surface)
	}
	if obs.Surface.Width <= 0 || obs.Surface.Height <= 0 {
		t.Errorf("surface dimensions = %dx%d, want positive", obs.Surface.Width, obs.Surface.Height)
	}
	if !strings.Contains(obs.VisibleText, "Submitted: Adax") || !strings.Contains(obs.VisibleText, "key:x") {
		t.Errorf("visible text = %q, want action results", obs.VisibleText)
	}
	if ready, ok := obs.Raw["ready_state"].(string); !ok || ready != "complete" {
		t.Errorf("raw ready_state = %#v, want complete", obs.Raw["ready_state"])
	}
	assertTarget(t, obs, "#name", "textbox", "Name", "Adax")
	assertPNGDataURI(t, obs)

	for name, req := range map[string]struct {
		req  computer.ActionRequest
		want string
	}{
		"navigate without URL": {
			req:  computer.ActionRequest{Action: computer.Action{Kind: computer.ActionKinds.NAVIGATE}},
			want: "navigate action requires url",
		},
		"press without key": {
			req:  computer.ActionRequest{Action: computer.Action{Kind: computer.ActionKinds.PRESS}},
			want: "press action requires key",
		},
		"missing target": {
			req: computer.ActionRequest{Action: computer.Action{
				Kind:   computer.ActionKinds.CLICK,
				Target: &computer.TargetRef{ID: "does-not-exist"},
			}},
			want: "target not found",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := session.Act(t.Context(), req.req)
			if err == nil || !strings.Contains(err.Error(), req.want) {
				t.Fatalf("Act error = %v, want text %q", err, req.want)
			}
		})
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = session.Act(canceled, computer.ActionRequest{
		When: &computer.Trigger{Kind: computer.TriggerKinds.TEXTPRESENT, Text: "never appears"},
		Action: computer.Action{
			Kind: computer.ActionKinds.SCROLL,
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Act with canceled context error = %v, want context.Canceled", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	closed = true
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := session.Observe(t.Context(), computer.ObserveRequest{}); err == nil {
		t.Fatal("Observe after Close returned nil error")
	}
}

func findChromeBinary(t *testing.T) string {
	t.Helper()

	if configured := os.Getenv("ZKIT_CHROME_PATH"); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			t.Skipf("Chrome integration skipped: ZKIT_CHROME_PATH %q is unavailable: %v", configured, err)
		}
		return path
	}
	for _, candidate := range []string{
		"headless_shell",
		"headless-shell",
		"chromium",
		"chromium-browser",
		"google-chrome",
		"google-chrome-stable",
		"chrome",
	} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("Chrome integration skipped: no Chrome/Chromium binary found; set ZKIT_CHROME_PATH")
	return ""
}

func newFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <title>Browser contract</title>
  <style>body { min-height: 1600px; } #panel { width: 120px; height: 30px; }</style>
</head>
<body>
  <h1>Fixture ready</h1>
  <label for="name">Name</label>
  <input id="name" aria-label="Name" placeholder="Your name">
  <button id="submit" onclick="document.getElementById('result').textContent = 'Submitted: ' + document.getElementById('name').value; location.hash = 'done'">Submit</button>
  <button id="hide" onclick="document.getElementById('panel').style.display = 'none'">Hide panel</button>
  <div id="panel">Visible panel</div>
  <output id="result"></output>
  <output id="key-result"></output>
  <script>
    document.getElementById('name').addEventListener('keydown', event => {
      document.getElementById('key-result').textContent = 'key:' + event.key;
    });
  </script>
</body>
</html>`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func assertTarget(t *testing.T, obs computer.Observation, id, role, name, value string) {
	t.Helper()

	for _, target := range obs.Targets {
		if target.ID != id {
			continue
		}
		if target.Role != role || target.Name != name || target.Value != value {
			t.Errorf("target %q = %#v, want role=%q name=%q value=%q", id, target, role, name, value)
		}
		if target.Bounds == nil || target.Bounds.Width <= 0 || target.Bounds.Height <= 0 || !target.Visible || !target.Enabled {
			t.Errorf("target %q geometry/state = %#v, want visible enabled positive bounds", id, target)
		}
		return
	}
	t.Errorf("target %q not found in %#v", id, obs.Targets)
}

func assertPNGDataURI(t *testing.T, obs computer.Observation) {
	t.Helper()

	if obs.Screenshot == nil {
		t.Fatal("screenshot is nil")
	}
	if obs.Screenshot.MIMEType != "image/png" {
		t.Errorf("screenshot MIME type = %q, want image/png", obs.Screenshot.MIMEType)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(obs.Screenshot.DataURI, prefix) {
		t.Fatalf("screenshot data URI lacks %q prefix", prefix)
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(obs.Screenshot.DataURI, prefix))
	if err != nil {
		t.Fatalf("decode screenshot: %v", err)
	}
	if len(png) < 8 || string(png[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("screenshot does not have PNG signature: %x", png)
	}
}
