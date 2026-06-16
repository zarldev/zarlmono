package zenv_test

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zenv"
)

func TestString(t *testing.T) {
	t.Setenv("ZENV_X_STR", "hello")
	if got := zenv.String("ZENV_X_STR", "def"); got != "hello" {
		t.Errorf("set: got %q, want hello", got)
	}
	t.Setenv("ZENV_X_STR", "")
	if got := zenv.String("ZENV_X_STR", "def"); got != "def" {
		t.Errorf("unset: got %q, want def", got)
	}
}

func TestInt(t *testing.T) {
	t.Setenv("ZENV_X_INT", "42")
	if got := zenv.Int("ZENV_X_INT", 7); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
	t.Setenv("ZENV_X_INT", "not-a-number")
	if got := zenv.Int("ZENV_X_INT", 7); got != 7 {
		t.Errorf("unparseable: got %d, want default 7", got)
	}
}

func TestInt64AndFloat64(t *testing.T) {
	t.Setenv("ZENV_X_I64", "9000000000")
	if got := zenv.Int64("ZENV_X_I64", 1); got != 9000000000 {
		t.Errorf("Int64: got %d", got)
	}
	t.Setenv("ZENV_X_F64", "3.14")
	if got := zenv.Float64("ZENV_X_F64", 0); got != 3.14 {
		t.Errorf("Float64: got %v", got)
	}
}

func TestDuration(t *testing.T) {
	t.Setenv("ZENV_X_DUR", "1m30s")
	if got := zenv.Duration("ZENV_X_DUR", 0); got != 90*time.Second {
		t.Errorf("got %v, want 90s", got)
	}
	t.Setenv("ZENV_X_DUR", "garbage")
	if got := zenv.Duration("ZENV_X_DUR", 5*time.Second); got != 5*time.Second {
		t.Errorf("unparseable: got %v, want 5s default", got)
	}
}

func TestBool(t *testing.T) {
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"true", false, true},
		{"TRUE", false, true},
		{"1", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"false", true, false},
		{"FALSE", true, false},
		{"0", true, false},
		{"no", true, false},
		{"off", true, false},
		// Garbage falls back to default.
		{"maybe", true, true},
		{"maybe", false, false},
	}
	for _, c := range cases {
		t.Setenv("ZENV_X_BOOL", c.in)
		if got := zenv.Bool("ZENV_X_BOOL", c.def); got != c.want {
			t.Errorf("Bool(%q, def=%v) = %v, want %v", c.in, c.def, got, c.want)
		}
	}
	// Unset → default.
	t.Setenv("ZENV_X_BOOL", "")
	if got := zenv.Bool("ZENV_X_BOOL", true); got != true {
		t.Errorf("unset should return default true")
	}
}

func TestGet_CustomType(t *testing.T) {
	// Demonstrates the generic primitive: a URL parser slots in
	// directly with no per-type plumbing in zenv.
	t.Setenv("ZENV_X_URL", "https://example.com/x")
	def, _ := url.Parse("http://localhost")
	got := zenv.Get("ZENV_X_URL", def, url.Parse)
	if got.Host != "example.com" {
		t.Errorf("custom parser: got host %q", got.Host)
	}
	t.Setenv("ZENV_X_URL", "::not a url::")
	got = zenv.Get("ZENV_X_URL", def, url.Parse)
	if got.Host != "localhost" {
		t.Errorf("parse error should return default; got host %q", got.Host)
	}
}

func TestGet_FailureAsDefault(t *testing.T) {
	// Parser that always errors should always return the default.
	parse := func(string) (int, error) { return 0, errors.New("boom") }
	t.Setenv("ZENV_X_FAIL", "anything")
	if got := zenv.Get("ZENV_X_FAIL", 99, parse); got != 99 {
		t.Errorf("got %d, want 99", got)
	}
}

func TestMustGet(t *testing.T) {
	t.Run("present and parseable returns value", func(t *testing.T) {
		t.Setenv("ZENV_X_MUST", "42")
		got, err := zenv.MustGet("ZENV_X_MUST", strconv.Atoi)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != 42 {
			t.Errorf("got %d, want 42", got)
		}
	})

	t.Run("unset returns ErrUnset", func(t *testing.T) {
		t.Setenv("ZENV_X_MUST", "")
		_, err := zenv.MustGet("ZENV_X_MUST", strconv.Atoi)
		if !errors.Is(err, zenv.ErrUnset) {
			t.Fatalf("err = %v, want errors.Is(err, ErrUnset)", err)
		}
	})

	t.Run("parse failure surfaces the parser error", func(t *testing.T) {
		t.Setenv("ZENV_X_MUST", "not-a-number")
		_, err := zenv.MustGet("ZENV_X_MUST", strconv.Atoi)
		if err == nil {
			t.Fatal("err = nil, want parse failure")
		}
		if errors.Is(err, zenv.ErrUnset) {
			t.Errorf("err wrongly classified as ErrUnset: %v", err)
		}
		// Error message should mention the env var name so a
		// startup failure is debuggable without grepping the parser.
		if got := err.Error(); !strings.Contains(got, "ZENV_X_MUST") {
			t.Errorf("err message %q does not mention the env var name", got)
		}
	})

	t.Run("custom parser composes the same way as Get", func(t *testing.T) {
		// Mirrors the vault use case: base64 decode of a 32-byte
		// secret. Parse failure must surface; absence is
		// distinguishable.
		parse := func(s string) ([]byte, error) {
			if s == "ok" {
				return []byte("decoded"), nil
			}
			return nil, errors.New("parse boom")
		}

		t.Setenv("ZENV_X_MUST_CUSTOM", "ok")
		got, err := zenv.MustGet("ZENV_X_MUST_CUSTOM", parse)
		if err != nil || string(got) != "decoded" {
			t.Errorf("custom ok: got (%q, %v)", got, err)
		}

		t.Setenv("ZENV_X_MUST_CUSTOM", "bad")
		_, err = zenv.MustGet("ZENV_X_MUST_CUSTOM", parse)
		if err == nil || errors.Is(err, zenv.ErrUnset) {
			t.Errorf("custom bad: err = %v, want non-nil non-ErrUnset", err)
		}
	})
}

// Sanity check: the named Int wrapper is a strict superset of using
// strconv.Atoi via Get — i.e. the wrapper isn't doing extra work.
func TestInt_EquivalentToGet(t *testing.T) {
	t.Setenv("ZENV_X_EQ", "123")
	if zenv.Int("ZENV_X_EQ", 0) != zenv.Get("ZENV_X_EQ", 0, strconv.Atoi) {
		t.Errorf("Int wrapper diverges from Get(strconv.Atoi)")
	}
}
