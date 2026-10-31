package repohealth_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/repohealth"
)

func TestCheckConfinesLocalLinkTargets(t *testing.T) {
	for _, name := range []string{"ordinary", "internal symlink", "external file symlink", "external directory symlink", "encoded traversal", "broken symlink"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "repo")
			writeFile(t, filepath.Join(root, ".zarlcode", "skills", "go-style", "SKILL.md"), "---\nname: go-style\ndescription: Go policy.\n---\nInstructions.\n")
			writeFile(t, filepath.Join(root, "docs", "target.md"), "Internal document.\n")
			writeFile(t, filepath.Join(base, "outside", "target.md"), "External document.\n")
			link := "docs/target.md"
			var diagnostic string
			switch name {
			case "internal symlink":
				link = "alias.md"
				makeLink(t, filepath.Join("docs", "target.md"), filepath.Join(root, link))
			case "external file symlink":
				link = "alias.md"
				makeLink(t, filepath.Join("..", "outside", "target.md"), filepath.Join(root, link))
				diagnostic = "does not resolve"
			case "external directory symlink":
				link = "alias/target.md"
				makeLink(t, filepath.Join("..", "outside"), filepath.Join(root, "alias"))
				diagnostic = "does not resolve"
			case "encoded traversal":
				link = "%2e%2e/outside/target.md"
				diagnostic = "escapes the repository"
			case "broken symlink":
				link = "alias.md"
				makeLink(t, filepath.Join("docs", "missing.md"), filepath.Join(root, link))
				diagnostic = "does not resolve"
			}
			writeFile(t, filepath.Join(root, "AGENTS.md"), "Use [go-style](.zarlcode/skills/go-style/SKILL.md).\nSee [document]("+link+").\n")
			err := repohealth.Check(root)
			if diagnostic == "" {
				if err != nil {
					t.Fatalf("valid local link: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), `local link "`+link+`" `+diagnostic) {
				t.Fatalf("Check error = %v; want %q for %q", err, diagnostic, link)
			}
		})
	}
}

func makeLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}
