package greet

import "testing"

func TestMessage(t *testing.T) {
	if got := Message("zarlcode"); got != "hello, zarlcode" {
		t.Fatalf("Message() = %q", got)
	}
}
