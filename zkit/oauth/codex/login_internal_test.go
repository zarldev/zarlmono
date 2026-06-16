package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// TestMakeOAuthHandler_HappyPath drives the callback handler with the
// canonical "code + correct state" callback the browser would send.
func TestMakeOAuthHandler_HappyPath(t *testing.T) {
	t.Parallel()
	resCh := make(chan callbackResult, 1)
	h := makeCallbackHandler("state-abc", resCh)

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/auth/callback?code=the-code&state=state-abc")
	if err != nil {
		t.Fatalf("get callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("res.err = %v", res.err)
		}
		if res.code != "the-code" {
			t.Errorf("code = %q, want the-code", res.code)
		}
	default:
		t.Fatalf("handler didn't push result")
	}
}

func TestMakeOAuthHandler_StateMismatchRejectsWithoutUnblocking(t *testing.T) {
	t.Parallel()
	resCh := make(chan callbackResult, 1)
	h := makeCallbackHandler("state-good", resCh)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/auth/callback?code=x&state=state-bad")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	select {
	case res := <-resCh:
		t.Fatalf("state mismatch should not unblock waiter, got %+v", res)
	default:
	}
}

// TestRunOAuthLoginManual_EndToEnd drives the manual-paste branch
// end-to-end: builds a fresh flow, pretends the user pasted the
// matching code+state, and verifies the persisted credential.
func TestRunOAuthLoginManual_EndToEnd(t *testing.T) {
	store, v := openTestStoreAndVault(t)

	jwt := makeJWTPayload(t, "acct_manual")
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("code") != "pasted-code" {
			http.Error(w, "wrong code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"`+jwt+`","refresh_token":"r-manual","expires_in":3600}`)
	}))
	defer tokenSrv.Close()

	// Redirect openaicodex's OAuth client onto the test server. The
	// package owns its own [zhttp.Client] now, so hijacking
	// http.DefaultClient doesn't reach the token-exchange path.
	redirectOAuthClient(t, mustURL(t, tokenSrv.URL))

	flow, err := openaicodex.CreateAuthorizationFlow()
	if err != nil {
		t.Fatalf("CreateAuthorizationFlow: %v", err)
	}

	// User pastes the redirect URL with the matching code+state.
	input := "http://localhost:1455/auth/callback?code=pasted-code&state=" + flow.State + "\n"

	svc := prefs.NewService(store, v, "")
	var stdout bytes.Buffer
	if err := runManual(context.Background(), svc, flow, strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("runManual: %v", err)
	}
	if !strings.Contains(stdout.String(), "acct_manual") {
		t.Errorf("stdout = %q, want it to mention account id", stdout.String())
	}

	// Vault should now carry the credential.
	stored, ok, err := svc.GetKey(context.Background(), prefs.ScopeGlobal, CredProvider)
	if err != nil || !ok {
		t.Fatalf("getStoredAPIKey: ok=%v err=%v", ok, err)
	}
	var c Cred
	if err := json.Unmarshal([]byte(stored), &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Refresh != "r-manual" || c.AccountID != "acct_manual" {
		t.Errorf("persisted cred = %+v", c)
	}
}

// TestRunOAuthLoginManual_StateMismatch ensures the manual path won't
// proceed if the pasted state doesn't match the flow we generated —
// even if the code looks plausible.
func TestRunOAuthLoginManual_StateMismatch(t *testing.T) {
	store, v := openTestStoreAndVault(t)
	flow, err := openaicodex.CreateAuthorizationFlow()
	if err != nil {
		t.Fatalf("CreateAuthorizationFlow: %v", err)
	}
	input := "http://localhost:1455/auth/callback?code=x&state=not-our-state\n"
	svc := prefs.NewService(store, v, "")
	err = runManual(context.Background(), svc, flow, strings.NewReader(input), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("err = %v, want state mismatch", err)
	}
}

func TestRunOAuthLoginManual_RawCodeRejected(t *testing.T) {
	store, v := openTestStoreAndVault(t)
	flow, err := openaicodex.CreateAuthorizationFlow()
	if err != nil {
		t.Fatalf("CreateAuthorizationFlow: %v", err)
	}
	svc := prefs.NewService(store, v, "")
	err = runManual(context.Background(), svc, flow, strings.NewReader("raw-code-only\n"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "state required") {
		t.Errorf("err = %v, want state required", err)
	}
}
