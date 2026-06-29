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
}
