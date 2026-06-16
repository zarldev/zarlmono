package repair_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
)

// repairTo is a tiny helper: run Repair, assert ok, parse back, and
// return the parsed object so tests can compare field-by-field.
func repairTo(t *testing.T, raw string) map[string]any {
	t.Helper()
	out, ok := repair.String(raw)
	if !ok {
		t.Fatalf("Repair(%q): ok=false, want repair to succeed (got %q)", raw, out)
	}
	var got map[string]any
	if err := repair.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("Repair(%q): repaired %q is not parseable: %v", raw, out, err)
	}
	return got
}

func TestRepair_DirectParsePasses(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{"path": "foo.go", "content": "hi"}`)
	if got["path"] != "foo.go" || got["content"] != "hi" {
		t.Errorf("got = %v", got)
	}
}

func TestRepair_EscapesLiteralNewlinesInStrings(t *testing.T) {
	t.Parallel()
	// The classic small-model failure mode: a multi-line string value
	// with literal \n instead of "\\n".
	raw := "{\"content\": \"line one\nline two\nline three\"}"
	got := repairTo(t, raw)
	if got["content"] != "line one\nline two\nline three" {
		t.Errorf("content = %q, want preserved newlines", got["content"])
	}
}

func TestRepair_EscapesLiteralTabsInStrings(t *testing.T) {
	t.Parallel()
	raw := "{\"k\": \"a\tb\"}"
	got := repairTo(t, raw)
	if got["k"] != "a\tb" {
		t.Errorf("k = %q, want 'a\\tb'", got["k"])
	}
}

func TestRepair_PreservesExistingEscapeSequences(t *testing.T) {
	t.Parallel()
	// `\"` and `\\` inside a string value must survive the escape walker
	// untouched; double-escaping would corrupt them.
	raw := `{"k": "a\"b\\c"}`
	got := repairTo(t, raw)
	if got["k"] != `a"b\c` {
		t.Errorf("k = %q, want 'a\"b\\c'", got["k"])
	}
}

func TestRepair_StripsTrailingCommas(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{"a": 1, "b": 2,}`)
	if got["a"] != 1.0 || got["b"] != 2.0 {
		t.Errorf("got = %v", got)
	}
}

func TestRepair_StripsTrailingCommasInArrays(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{"xs": [1, 2, 3,]}`)
	xs, ok := got["xs"].([]any)
	if !ok || len(xs) != 3 {
		t.Errorf("xs = %v, want 3-element array", got["xs"])
	}
}

func TestRepair_SingleToDoubleQuotes(t *testing.T) {
	t.Parallel()
	// Conservative: only converts when no double quotes present.
	got := repairTo(t, `{'path': 'foo.go'}`)
	if got["path"] != "foo.go" {
		t.Errorf("path = %v", got["path"])
	}
}

func TestRepair_LeavesValidJSONWithApostrophesAlone(t *testing.T) {
	t.Parallel()
	// Already-valid JSON with apostrophes inside a string must not
	// get its inner `'` clobbered by the single→double pass.
	got := repairTo(t, `{"note": "it's fine"}`)
	if got["note"] != "it's fine" {
		t.Errorf("note = %q", got["note"])
	}
}

func TestRepair_QuotesUnquotedKeys(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{path: "foo.go", content: "hi"}`)
	if got["path"] != "foo.go" || got["content"] != "hi" {
		t.Errorf("got = %v", got)
	}
}

func TestRepair_BalancesMissingObjectCloser(t *testing.T) {
	t.Parallel()
	// max_tokens truncation: the closer is missing.
	got := repairTo(t, `{"path": "foo.go", "content": "hi"`)
	if got["path"] != "foo.go" || got["content"] != "hi" {
		t.Errorf("got = %v", got)
	}
}

func TestRepair_BalancesMissingArrayCloser(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{"xs": [1, 2, 3`)
	xs, ok := got["xs"].([]any)
	if !ok || len(xs) != 3 {
		t.Errorf("xs = %v, want 3-element array", got["xs"])
	}
}

func TestRepair_BalancesNestedClosers(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{"outer": {"inner": "v"`)
	outer, ok := got["outer"].(map[string]any)
	if !ok || outer["inner"] != "v" {
		t.Errorf("outer = %v", got["outer"])
	}
}

func TestRepair_ClosesStringTruncatedMidValue(t *testing.T) {
	t.Parallel()
	// max_tokens truncation usually cuts inside a string value, not
	// neatly between tokens.
	got := repairTo(t, `{"path": "src/main.go", "content": "package ma`)
	if got["path"] != "src/main.go" {
		t.Errorf("path = %v", got["path"])
	}
	if got["content"] != "package ma" {
		t.Errorf("content = %v, want the truncated prefix preserved", got["content"])
	}
}

func TestRepair_ClosesStringTruncatedInsideNestedArray(t *testing.T) {
	t.Parallel()
	got := repairTo(t, `{"files": ["a.go", "b.g`)
	files, ok := got["files"].([]any)
	if !ok || len(files) != 2 || files[1] != "b.g" {
		t.Errorf("files = %v, want [a.go b.g]", got["files"])
	}
}

func TestRepair_TruncatedAfterTrailingBackslashStaysFailed(t *testing.T) {
	t.Parallel()
	// A string cut immediately after a backslash can't be closed by
	// appending a quote (it would just escape it); the repair must
	// report failure rather than fabricate a parse.
	if out, ok := repair.String(`{"path": "C:\`); ok {
		t.Errorf("Repair: want ok=false for trailing backslash, got %q", out)
	}
}

func TestRepair_ExtractsFlatObjectFromGarbage(t *testing.T) {
	t.Parallel()
	// Last-ditch: chatty model prefixes the JSON with a sentence.
	got := repairTo(t, `Sure, here you go: {"path": "foo.go"} — anything else?`)
	if got["path"] != "foo.go" {
		t.Errorf("path = %v", got["path"])
	}
}

func TestRepair_ReturnsFalseOnTotalGarbage(t *testing.T) {
	t.Parallel()
	out, ok := repair.String("not json at all, no objects here")
	if ok {
		t.Errorf("Repair(garbage): want ok=false, got %q", out)
	}
}

func TestRepair_EmptyInputReturnsFalse(t *testing.T) {
	t.Parallel()
	_, ok := repair.String("")
	if ok {
		t.Errorf("Repair(\"\"): want ok=false on empty")
	}
}

// --- Unmarshal convenience tests ---

func TestUnmarshal_PlainPathStillWorks(t *testing.T) {
	t.Parallel()
	var got struct {
		Path string `json:"path"`
	}
	if err := repair.Unmarshal([]byte(`{"path": "foo.go"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "foo.go" {
		t.Errorf("Path = %q", got.Path)
	}
}

func TestUnmarshal_RepairsAndDecodes(t *testing.T) {
	t.Parallel()
	var got struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	// Has literal newlines AND a missing closer — exercises two
	// repair steps in sequence.
	raw := "{\"path\": \"foo.go\", \"content\": \"line one\nline two\""
	if err := repair.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Path != "foo.go" {
		t.Errorf("Path = %q", got.Path)
	}
	if got.Content != "line one\nline two" {
		t.Errorf("Content = %q", got.Content)
	}
}

func TestUnmarshal_EmptyInputDecodesAsEmptyObject(t *testing.T) {
	t.Parallel()
	// Mirrors the runner's previous "skip Unmarshal if Arguments==\"\""
	// branch: callers don't have to special-case empty input.
	var got map[string]any
	if err := repair.Unmarshal([]byte(""), &got); err != nil {
		t.Fatalf("Unmarshal(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %v, want empty map", got)
	}
}

func TestUnmarshal_TotalFailureReturnsPlainParseError(t *testing.T) {
	t.Parallel()
	// Repair can't save unparseable input; the returned error should
	// be the strict json.Unmarshal error (informative about the
	// original input), not a repair-attempt error.
	var got map[string]any
	err := repair.Unmarshal([]byte("not json"), &got)
	if err == nil {
		t.Fatal("Unmarshal(garbage): want error")
	}
}
