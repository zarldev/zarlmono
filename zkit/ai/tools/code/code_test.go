package code_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func mustWS(t *testing.T) (code.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return ws, ws.Root()
}

func TestWriteThenReadRoundTrip(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	w := code.NewWriteTool(ws)
	r := code.NewReadTool(ws)

	res := execTyped(t, w, code.WriteArgs{Path: "hello.txt", Content: "line1\nline2\nline3\n"})
	if !res.Success {
		t.Fatalf("write failed: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "hello.txt")); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	res = execTyped(t, r, code.ReadArgs{Path: "hello.txt"})
	if !res.Success {
		t.Fatalf("read failed: %s", res.Error)
	}
	body, _ := res.Data.(string)
	if !strings.Contains(body, "1\tline1") || !strings.Contains(body, "3\tline3") {
		t.Errorf("read output missing line numbering:\n%s", body)
	}
}

func TestEditTool(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	tool := code.NewEditTool(ws)

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(root, name))
		return string(b)
	}

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		write("a.txt", "alpha\nbeta\ngamma\n")
		res := execTyped(t, tool, code.EditArgs{Path: "a.txt", OldString: "beta", NewString: "BETA"})
		if !res.Success {
			t.Fatalf("unexpected failure: %s", res.Error)
		}
		if read("a.txt") != "alpha\nBETA\ngamma\n" {
			t.Fatalf("not replaced: %q", read("a.txt"))
		}
	})

	t.Run("ambiguous match rejected", func(t *testing.T) {
		t.Parallel()
		write("c.txt", "x\nx\n")
		res := execTyped(t, tool, code.EditArgs{Path: "c.txt", OldString: "x", NewString: "y"})
		if res.Success {
			t.Fatal("expected ambiguous-match failure")
		}
	})

	t.Run("replace_all", func(t *testing.T) {
		t.Parallel()
		write("d.txt", "x\nx\nx\n")
		res := execTyped(t, tool, code.EditArgs{Path: "d.txt", OldString: "x", NewString: "y", ReplaceAll: true})
		if !res.Success {
			t.Fatalf("unexpected failure: %s", res.Error)
		}
		if read("d.txt") != "y\ny\ny\n" {
			t.Fatalf("replace_all wrong: %q", read("d.txt"))
		}
	})

	t.Run("fuzzy trailing whitespace", func(t *testing.T) {
		t.Parallel()
		// File has trailing spaces on the line; old_string doesn't.
		write("fz1.txt", "alpha\nbeta  \ngamma\n")
		res := execTyped(t, tool, code.EditArgs{Path: "fz1.txt", OldString: "beta", NewString: "BETA"})
		if !res.Success {
			t.Fatalf("unexpected failure: %s", res.Error)
		}
		// Trailing spaces preserved by exact-match path (it actually
		// matches "beta" inside "beta  "). Use a stronger test below.
		_ = read
	})

	t.Run("fuzzy old has trailing whitespace file doesnt", func(t *testing.T) {
		t.Parallel()
		write("fz2.txt", "alpha\nbeta\ngamma\n")
		res := execTyped(t, tool, code.EditArgs{Path: "fz2.txt", OldString: "beta   ", NewString: "BETA"})
		if !res.Success {
			t.Fatalf("unexpected failure: %s", res.Error)
		}
		if read("fz2.txt") != "alpha\nBETA\ngamma\n" {
			t.Fatalf("not replaced: %q", read("fz2.txt"))
		}
		if !strings.Contains(res.Data.(string), "whitespace normalisation") {
			t.Errorf("expected fuzzy-path note in result; got: %v", res.Data)
		}
	})

	t.Run("fuzzy multiline mixed line endings", func(t *testing.T) {
		t.Parallel()
		// File uses CRLF; old_string uses LF.
		write("fz3.txt", "one\r\ntwo\r\nthree\r\n")
		res := execTyped(t, tool, code.EditArgs{Path: "fz3.txt", OldString: "two\nthree", NewString: "TWO\nTHREE"})
		if !res.Success {
			t.Fatalf("unexpected failure: %s", res.Error)
		}
		// The body's original CRLF on the matched lines should be
		// replaced; the final \r\n at EOF stays because old had no
		// trailing newline.
		got := read("fz3.txt")
		if got != "one\r\nTWO\nTHREE\r\n" {
			t.Fatalf("CRLF splice wrong: %q", got)
		}
	})

	t.Run("fuzzy refuses ambiguous", func(t *testing.T) {
		t.Parallel()
		// Body has two lines that both normalise to "foo"; neither
		// line literally contains the old_string "foo\t" (no tabs in
		// body), so the exact path returns 0 and the fuzzy path
		// then sees 2 matches → refuses.
		write("fz4.txt", "foo  \nbar\nfoo \nbar\n")
		res := execTyped(t, tool, code.EditArgs{Path: "fz4.txt", OldString: "foo\t", NewString: "FOO"})
		if res.Success {
			t.Fatal("expected ambiguous-fuzzy failure")
		}
		if !strings.Contains(res.Error, "after whitespace normalisation") {
			t.Errorf("expected ambiguity error to mention normalisation; got: %s", res.Error)
		}
	})

	t.Run("not found hints at whitespace mismatch", func(t *testing.T) {
		t.Parallel()
		write("fz5.txt", "\t\treturn err\n")
		// typo in 'errr' so fuzzy also misses
		res := execTyped(
			t,
			tool,
			code.EditArgs{Path: "fz5.txt", OldString: "        return errr", NewString: "        return nil"},
		)
		if res.Success {
			t.Fatal("expected not-found failure")
		}
		// No matching content; hint should be empty (no fake match).
		if strings.Contains(res.Error, "matching content") {
			t.Errorf("unexpected hint on a true miss: %s", res.Error)
		}
	})

	t.Run("not found WITH hint on indentation mismatch", func(t *testing.T) {
		t.Parallel()
		write("fz6.txt", "\t\treturn err\n")
		// Same content, different indent (spaces vs tabs) — fuzzy won't
		// help (interior whitespace isn't normalised) but the hint
		// should point at the line.
		res := execTyped(
			t,
			tool,
			code.EditArgs{Path: "fz6.txt", OldString: "        return err", NewString: "        return nil"},
		)
		if res.Success {
			t.Fatal("expected not-found failure")
		}
		if !strings.Contains(res.Error, "matching content but different whitespace") {
			t.Errorf("expected indentation hint; got: %s", res.Error)
		}
	})
}

func TestLsTool(t *testing.T) {
	t.Parallel()
	ws, root := mustWS(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := execTyped(t, code.NewLsTool(ws), code.LsArgs{Output: tools.OutputJSON})
	if !res.Success {
		t.Fatalf("ls failed: %s", res.Error)
	}
	result, ok := res.Data.(code.LsResult)
	if !ok {
		t.Fatalf("Data is %T, want code.LsResult", res.Data)
	}
	body := result.String()

	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("ls did not return JSON: %v\n%s", err, body)
	}
	names := map[string]string{}
	for _, e := range entries {
		names[e.Name] = e.Type
	}
	if names["a.txt"] != "file" || names["sub"] != "dir" {
		t.Errorf("missing entries: %v", names)
	}
	if _, ok := names[".hidden"]; ok {
		t.Errorf("hidden entry leaked without show_hidden=true")
	}
}

func TestBashTool_RunsCommand(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	res := execTyped(t, code.NewBashTool(ws), code.BashArgs{Command: "echo hello && echo world"})
	if !res.Success {
		t.Fatalf("bash failed: %s", res.Error)
	}
	out, _ := res.Data.(string)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("expected hello+world in output, got %q", out)
	}
	if !strings.Contains(out, "[exit 0]") {
		t.Errorf("expected exit marker, got %q", out)
	}
}

func TestBashTool_NonzeroExitReportedAsSuccess(t *testing.T) {
	t.Parallel()
	ws, _ := mustWS(t)
	res := execTyped(t, code.NewBashTool(ws), code.BashArgs{Command: "exit 7"})
	if !res.Success {
		t.Fatalf("bash should succeed even when command exits nonzero, got error %s", res.Error)
	}
	out, _ := res.Data.(string)
	if !strings.Contains(out, "[exit 7]") {
		t.Errorf("expected exit 7 marker, got %q", out)
	}
}
