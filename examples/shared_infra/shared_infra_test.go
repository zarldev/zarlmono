package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSharedInfra(t *testing.T) {
	var buf bytes.Buffer
	if err := run(t.Context(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"kind":"workflow_started"`) {
		t.Fatalf("missing trace output: %s", out)
	}
	if !strings.Contains(out, "checkpoint=draft-1") || !strings.Contains(out, "review=approve") {
		t.Fatalf("missing checkpoint/review summary: %s", out)
	}

	// Deterministic code-understanding tools: file_map outlines declarations
	// and retrieve_code ranks the Save method first for the query.
	for _, want := range []string{
		"== file_map ==",
		"store.go  package sample",
		"method (*Store).Save :: func (s *Store) Save(key, value string)",
		`== retrieve_code: "save value to store" ==`,
		"[1] store.go",
		"method Save",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing code-understanding output %q:\n%s", want, out)
		}
	}
}
