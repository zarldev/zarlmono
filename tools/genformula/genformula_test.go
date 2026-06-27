package genformula_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/genformula"
)

func validChecksums(version string) map[string]string {
	return map[string]string{
		"zarlcode_" + version + "_darwin_amd64.tar.gz": "aaaa",
		"zarlcode_" + version + "_darwin_arm64.tar.gz": "bbbb",
		"zarlcode_" + version + "_linux_amd64.tar.gz":  "cccc",
		"zarlcode_" + version + "_linux_arm64.tar.gz":  "dddd",
	}
}

func TestParseChecksums(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{
			name:  "two space separated",
			input: "aaaa  zarlcode_v0.1.2_linux_amd64.tar.gz\n",
			want:  map[string]string{"zarlcode_v0.1.2_linux_amd64.tar.gz": "aaaa"},
		},
		{
			name:  "skips blank lines",
			input: "\naaaa file_a\n\nbbbb file_b\n\n",
			want:  map[string]string{"file_a": "aaaa", "file_b": "bbbb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := genformula.ParseChecksums(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("ParseChecksums: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries, want %d", len(got), len(tt.want))
			}
			for name, sha := range tt.want {
				if got[name] != sha {
					t.Errorf("%s: got %q, want %q", name, got[name], sha)
				}
			}
		})
	}
}

func TestRender(t *testing.T) {
	out, err := genformula.Render("v0.1.2", validChecksums("v0.1.2"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	wants := []string{
		`version "0.1.2"`,
		`releases/download/zarlcode/v0.1.2/zarlcode_v0.1.2_darwin_arm64.tar.gz`,
		`sha256 "bbbb"`,
		`releases/download/zarlcode/v0.1.2/zarlcode_v0.1.2_linux_amd64.tar.gz`,
		`sha256 "cccc"`,
		`system "#{bin}/zarlcode", "-version"`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("rendered formula missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderMissingChecksum(t *testing.T) {
	sums := validChecksums("v0.1.2")
	delete(sums, "zarlcode_v0.1.2_linux_arm64.tar.gz")

	_, err := genformula.Render("v0.1.2", sums)
	if err == nil {
		t.Fatal("Render: expected error for missing checksum, got nil")
	}
	if !strings.Contains(err.Error(), "linux_arm64") {
		t.Errorf("error should name the missing archive: %v", err)
	}
}
