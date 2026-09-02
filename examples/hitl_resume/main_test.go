package main_test

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestDecisionsResumeFromCheckpoint(t *testing.T) {
	tests := []struct {
		decision string
		want     string
	}{
		{decision: "approve", want: "result status=deployed target=production"},
		{decision: "deny", want: "result status=cancelled target=production"},
		{decision: "edit", want: "result status=deployed target=staging"},
	}
	for _, tt := range tests {
		t.Run(tt.decision, func(t *testing.T) {
			cmd := exec.Command("go", "run", ".", "-decision="+tt.decision)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run example: %v\n%s", err, output)
			}
			got := string(output)
			for _, want := range []string{
				"paused request=deploy-42-review risk=high decided=false checkpoint=deploy-42-before-review",
				"loaded checkpoint=deploy-42-before-review step=before-production-deploy decision=" + tt.decision,
				tt.want,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("output %q does not contain %q", got, want)
				}
			}
		})
	}
}

func TestOmittedDecisionWaitsForHumanInput(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("open stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start interactive example: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	scanner := bufio.NewScanner(stdout)
	for _, want := range []string{
		"paused request=deploy-42-review",
		"awaiting human decision [approve|deny|edit]",
	} {
		if !scanner.Scan() {
			t.Fatalf("read prompt: %v\n%s", scanner.Err(), stderr.String())
		}
		if got := scanner.Text(); !strings.Contains(got, want) {
			t.Fatalf("output %q does not contain %q", got, want)
		}
	}

	if _, err := fmt.Fprintln(stdin, "edit"); err != nil {
		t.Fatalf("submit decision: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	var resumed strings.Builder
	for scanner.Scan() {
		fmt.Fprintln(&resumed, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read resumed output: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("run interactive example: %v\n%s", err, stderr.String())
	}
	for _, want := range []string{
		"loaded checkpoint=deploy-42-before-review step=before-production-deploy decision=edit",
		"result status=deployed target=staging",
	} {
		if !strings.Contains(resumed.String(), want) {
			t.Errorf("output %q does not contain %q", resumed.String(), want)
		}
	}
}
