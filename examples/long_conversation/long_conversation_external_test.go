package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestLongConversationScripted(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "-scripted", "-summary")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run long_conversation: %v\n%s", err, out)
	}

	text := string(out)
	for _, want := range []string{
		"status=succeeded",
		"Research summary: files=3",
		"functions=17",
		"GetUserHandler",
		"CreateUserHandler",
		"UpdateUserHandler",
		"DeleteUserHandler",
		"ListUsersHandler",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}
