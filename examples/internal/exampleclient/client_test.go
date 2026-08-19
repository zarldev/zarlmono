package exampleclient_test

import (
	"testing"

	"github.com/zarldev/zarlmono/examples/internal/exampleclient"
)

func TestBaseURL(t *testing.T) {
	t.Setenv("LLAMACPP_BASE_URL", "http://env.example/v1")

	if got := exampleclient.BaseURL("http://flag.example/v1", "llamacpp"); got != "http://flag.example/v1" {
		t.Fatalf("explicit base URL = %q", got)
	}
	if got := exampleclient.BaseURL("", "llamacpp"); got != "http://env.example/v1" {
		t.Fatalf("environment base URL = %q", got)
	}
	if got := exampleclient.BaseURL("", "openai"); got != "" {
		t.Fatalf("openai base URL = %q, want empty", got)
	}
}
