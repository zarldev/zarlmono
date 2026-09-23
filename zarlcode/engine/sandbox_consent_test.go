package engine_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

func withoutSandboxOverride(t *testing.T) {
	t.Helper()
	t.Setenv("ZARLCODE_SANDBOX", "")
	if err := os.Unsetenv("ZARLCODE_SANDBOX"); err != nil {
		t.Fatal(err)
	}
}

func TestResolveShellSandboxPlatformAndPreference(t *testing.T) {
	for _, tt := range []struct {
		name, platform, global, workspace string
		wantEnabled, wantConsent          bool
	}{
		{name: "fresh Linux", platform: "linux", wantEnabled: true},
		{name: "fresh Darwin", platform: "darwin", wantConsent: true},
		{name: "other platforms fail closed", platform: "windows", wantEnabled: true},
		{name: "Darwin explicit global on", platform: "darwin", global: "on", wantEnabled: true},
		{name: "Darwin explicit global off", platform: "darwin", global: "off"},
		{name: "Darwin workspace opt in", platform: "darwin", global: "off", workspace: "on", wantEnabled: true},
		{name: "Darwin workspace opt out", platform: "darwin", global: "on", workspace: "off"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withoutSandboxOverride(t)
			s := newGuardrailSettings(t)
			for scope, value := range map[prefs.Scope]string{prefs.ScopeGlobal: tt.global, prefs.ScopeWorkspace: tt.workspace} {
				if value != "" {
					if err := s.Svc.SetSetting(t.Context(), scope, prefs.KeySandbox, value); err != nil {
						t.Fatal(err)
					}
				}
			}
			prompted := false
			enabled, err := s.ResolveShellSandbox(t.Context(), tt.platform, func(context.Context) (bool, error) {
				prompted = true
				return true, nil
			})
			if err != nil || enabled != tt.wantEnabled || prompted != tt.wantConsent {
				t.Fatalf("enabled=%v prompted=%v err=%v", enabled, prompted, err)
			}
			if tt.wantConsent {
				if value, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeySandbox); err != nil || value.Value != "off" {
					t.Fatalf("consent not saved to workspace: %q %v", value.Value, err)
				}
				if _, err := s.Svc.GetSetting(t.Context(), prefs.ScopeGlobal, prefs.KeySandbox); !errors.Is(err, prefs.ErrNotFound) {
					t.Fatalf("consent changed global policy: %v", err)
				}
				if s.ShellSandbox(t.Context()) {
					t.Fatal("startup choice disagrees with effective setting")
				}
			}
			if _, err := s.ResolveShellSandbox(t.Context(), tt.platform, func(context.Context) (bool, error) {
				t.Fatal("repeat startup prompted despite resolved preference")
				return false, nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResolveShellSandboxEnvironmentWins(t *testing.T) {
	for _, value := range []string{"off", "0", "false", "no", "on", "1", ""} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ZARLCODE_SANDBOX", value)
			s := newGuardrailSettings(t)
			want := value == "on" || value == "1" || value == ""
			setting := "on"
			if want {
				setting = "off"
			}
			if err := s.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeySandbox, setting); err != nil {
				t.Fatal(err)
			}
			enabled, err := s.ResolveShellSandbox(t.Context(), "darwin", func(context.Context) (bool, error) {
				t.Fatal("explicit environment choice prompted")
				return false, nil
			})
			if err != nil || enabled != want {
				t.Fatalf("enabled=%v want=%v err=%v", enabled, want, err)
			}
			if got, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeySandbox); err != nil || got.Value != setting {
				t.Fatalf("environment override persisted: %q %v", got.Value, err)
			}
		})
	}
}

func TestResolveShellSandboxDeclineAndPromptErrorDoNotPersist(t *testing.T) {
	promptErr := errors.New("no interactive terminal")
	for _, tt := range []struct {
		name string
		err  error
		want error
	}{
		{name: "declined", want: context.Canceled},
		{name: "headless or terminal failure", err: promptErr, want: promptErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withoutSandboxOverride(t)
			s := newGuardrailSettings(t)
			enabled, err := s.ResolveShellSandbox(t.Context(), "darwin", func(context.Context) (bool, error) {
				return false, tt.err
			})
			if enabled || !errors.Is(err, tt.want) {
				t.Fatalf("enabled=%v err=%v", enabled, err)
			}
			if _, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeySandbox); !errors.Is(err, prefs.ErrNotFound) {
				t.Fatalf("unsuccessful consent persisted: %v", err)
			}
		})
	}
}

func TestResolveShellSandboxStorageFailureDoesNotDisable(t *testing.T) {
	withoutSandboxOverride(t)
	s := newGuardrailSettings(t)
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveShellSandbox(t.Context(), "darwin", func(context.Context) (bool, error) {
		t.Fatal("failed preference read treated as unset")
		return true, nil
	}); err == nil {
		t.Fatal("storage failure allowed launch")
	}
}

func TestResolveShellSandboxFailedConsentWriteBlocksLaunch(t *testing.T) {
	withoutSandboxOverride(t)
	s := newGuardrailSettings(t)
	if _, err := s.ResolveShellSandbox(t.Context(), "darwin", func(context.Context) (bool, error) {
		if err := s.Store.Close(); err != nil {
			t.Fatal(err)
		}
		return true, nil
	}); err == nil {
		t.Fatal("failed consent persistence allowed launch")
	}
}
