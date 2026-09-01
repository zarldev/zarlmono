package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSharedInfra(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shared_infra: %v\n%s", err, out)
	}

	text := string(out)
	for _, want := range []string{
		"checkpoint=draft-1",
		"review=approve",
		"== file_map ==",
		"store.go  package sample",
		"method (*Store).Save :: func (s *Store) Save(key, value string)",
		`== retrieve_code: "save value to store" ==`,
		"[1] [definition] store.go",
		"method Save",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}
