package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/cli"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

func TestKeysCommandSetListDeleteUsesGlobalScopeAndRedactsSecrets(t *testing.T) {
	svc := prefs.NewService(openTestStore(t), nil, t.TempDir())
	cmd := cli.KeysCommand{Service: svc}
	secret := "sk-super-secret"

	code, stdout, stderr := executeKeys(t, cmd, "set", " OpenAI ", " "+secret+" ")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "stored api key for \"openai\" globally") {
		t.Fatalf("set: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertRedacted(t, secret, stdout, stderr)
	got, err := svc.GetKey(t.Context(), prefs.ScopeGlobal, "openai")
	if err != nil || got != secret {
		t.Fatalf("global key = %q, %v", got, err)
	}
	if _, err := svc.GetKey(t.Context(), prefs.ScopeWorkspace, "openai"); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("workspace key error = %v, want ErrNotFound", err)
	}

	code, stdout, stderr = executeKeys(t, cmd, "list")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "stored api keys (global scope):\n  - openai\n") {
		t.Fatalf("list: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertRedacted(t, secret, stdout, stderr)

	code, stdout, stderr = executeKeys(t, cmd, "delete", " OPENAI ")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "removed api key for \"openai\" from global scope") {
		t.Fatalf("delete: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assertRedacted(t, secret, stdout, stderr)
	if _, err := svc.GetKey(t.Context(), prefs.ScopeGlobal, "openai"); !errors.Is(err, prefs.ErrNotFound) {
		t.Fatalf("global key after delete error = %v, want ErrNotFound", err)
	}
}

func TestKeysCommandProtectStatus(t *testing.T) {
	cmd := cli.KeysCommand{Service: prefs.NewService(openTestStore(t), nil, "")}
	for _, args := range [][]string{{"protect"}, {"protect", "status"}} {
		code, stdout, stderr := executeKeys(t, cmd, args...)
		if code != 0 || stdout != "credential protection: off\n" || stderr != "" {
			t.Fatalf("%v: exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestKeysCommandRejectsInvalidCommandsAndProviders(t *testing.T) {
	cmd := cli.KeysCommand{Service: prefs.NewService(openTestStore(t), nil, "")}
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"wat"}, 2, "unknown subcommand"},
		{[]string{"set", " ", "secret"}, 2, "provider name is empty"},
		{[]string{"set", "openai", " "}, 2, "key is empty"},
		{[]string{"delete", " "}, 2, "provider name is empty"},
		{[]string{"protect", "wat"}, 2, "usage: zarlcode keys protect"},
		{[]string{"oauth", "unsupported-provider"}, 1, "is not supported"},
	}
	for _, tc := range cases {
		code, stdout, stderr := executeKeys(t, cmd, tc.args...)
		if code != tc.code || stdout != "" || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q", tc.args, code, stdout, stderr)
		}
		assertRedacted(t, "secret", stdout, stderr)
	}
}

func TestKeysCommandDispatchesOAuthWithoutLiveProvider(t *testing.T) {
	svc := prefs.NewService(openTestStore(t), nil, "")
	stdin := strings.NewReader("callback input")
	var gotProvider, gotInput string
	var gotService *prefs.Service
	login := func(_ context.Context, service *prefs.Service, provider string, in io.Reader, out io.Writer) error {
		gotService = service
		gotProvider = provider
		data, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		gotInput = string(data)
		_, err = io.WriteString(out, "oauth dispatched\n")
		return err
	}
	cmd := cli.KeysCommand{Service: svc, Stdin: stdin, OAuthLogin: login}
	code, stdout, stderr := executeKeys(t, cmd, "oauth", " OPENAI-CODEX ")
	if code != 0 || stdout != "oauth dispatched\n" || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if gotService != svc || gotProvider != "openai-codex" || gotInput != "callback input" {
		t.Fatalf("dispatch: service=%p provider=%q input=%q", gotService, gotProvider, gotInput)
	}
}

func executeKeys(t *testing.T, cmd cli.KeysCommand, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmd.Execute(t.Context(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func assertRedacted(t *testing.T, secret string, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, secret) {
			t.Fatalf("secret leaked in output %q", output)
		}
	}
}
