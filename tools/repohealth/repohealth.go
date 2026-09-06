// Package repohealth validates repository-owned agent guidance.
package repohealth

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	portableName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Mode        string `yaml:"mode"`
}

// Check validates repository-local skills, agents, and local Markdown links.
func Check(root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}

	var problems []error
	skills, skillFiles := checkSkills(absRoot, &problems)
	checkAgents(absRoot, &problems)
	guidanceFiles := append(listInstructionFiles(absRoot, &problems), skillFiles...)
	for _, path := range guidanceFiles {
		checkLinks(absRoot, path, &problems)
	}
	checkSkillRouting(absRoot, skills, &problems)
	return errors.Join(problems...)
}

func checkSkills(root string, problems *[]error) (map[string]string, []string) {
	dir := filepath.Join(root, ".zarlcode", "skills")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		*problems = append(*problems, errors.New("repository has no .zarlcode/skills directory"))
		return nil, nil
	}
	if err != nil {
		*problems = append(*problems, fmt.Errorf("read skill directory: %w", err))
		return nil, nil
	}

	skills := make(map[string]string)
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "SKILL.md")
		fm, body, err := readFrontmatter(path)
		if err != nil {
			*problems = append(*problems, fmt.Errorf("%s: %w", relative(root, path), err))
			continue
		}
		files = append(files, path)
		if !portableName.MatchString(fm.Name) || len(fm.Name) > 64 {
			*problems = append(*problems, fmt.Errorf("%s: invalid portable skill name %q", relative(root, path), fm.Name))
		}
		if fm.Name != entry.Name() {
			*problems = append(*problems, fmt.Errorf("%s: frontmatter name %q does not match directory %q", relative(root, path), fm.Name, entry.Name()))
		}
		if strings.TrimSpace(fm.Description) == "" {
			*problems = append(*problems, fmt.Errorf("%s: frontmatter description is required", relative(root, path)))
		}
		if strings.TrimSpace(body) == "" {
			*problems = append(*problems, fmt.Errorf("%s: skill instructions are required", relative(root, path)))
		}
		if previous, exists := skills[fm.Name]; exists {
			*problems = append(*problems, fmt.Errorf("%s: duplicate skill name %q also used by %s", relative(root, path), fm.Name, previous))
		}
		skills[fm.Name] = relative(root, path)
	}
	return skills, files
}

func checkAgents(root string, problems *[]error) {
	dir := filepath.Join(root, ".zarlcode", "agents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		*problems = append(*problems, fmt.Errorf("read agent directory: %w", err))
		return
	}
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fm, body, err := readFrontmatter(path)
		if err != nil {
			*problems = append(*problems, fmt.Errorf("%s: %w", relative(root, path), err))
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !portableName.MatchString(fm.Name) || fm.Name != stem {
			*problems = append(*problems, fmt.Errorf("%s: frontmatter name %q must match portable filename %q", relative(root, path), fm.Name, stem))
		}
		if strings.TrimSpace(fm.Description) == "" {
			*problems = append(*problems, fmt.Errorf("%s: frontmatter description is required", relative(root, path)))
		}
		switch strings.TrimSpace(fm.Mode) {
		case "", "explore", "verify", "implement":
		default:
			*problems = append(*problems, fmt.Errorf("%s: unsupported mode %q", relative(root, path), fm.Mode))
		}
		if strings.TrimSpace(body) == "" {
			*problems = append(*problems, fmt.Errorf("%s: agent instructions are required", relative(root, path)))
		}
		if previous, exists := seen[fm.Name]; exists {
			*problems = append(*problems, fmt.Errorf("%s: duplicate agent name %q also used by %s", relative(root, path), fm.Name, previous))
		}
		seen[fm.Name] = relative(root, path)
	}
}

func checkSkillRouting(root string, skills map[string]string, problems *[]error) {
	body, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		*problems = append(*problems, fmt.Errorf("read root AGENTS.md: %w", err))
		return
	}
	for name, path := range skills {
		want := ".zarlcode/skills/" + name + "/SKILL.md"
		if !strings.Contains(string(body), "("+want+")") {
			*problems = append(*problems, fmt.Errorf("AGENTS.md: repository skill %q at %s is not linked by the skill policy", name, path))
		}
	}
}

func listInstructionFiles(root string, problems *[]error) []string {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			*problems = append(*problems, fmt.Errorf("walk %s: %w", relative(root, path), walkErr))
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if shouldSkip(root, path, entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() == "AGENTS.md" || entry.Name() == "CLAUDE.md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		*problems = append(*problems, fmt.Errorf("walk repository guidance: %w", err))
	}
	return files
}

func checkLinks(root, source string, problems *[]error) {
	body, err := os.ReadFile(source)
	if err != nil {
		*problems = append(*problems, fmt.Errorf("read %s: %w", relative(root, source), err))
		return
	}
	for _, match := range markdownLink.FindAllSubmatch(body, -1) {
		raw := string(match[1])
		if strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "mailto:") || strings.Contains(raw, "://") {
			continue
		}
		if !strings.Contains(raw, "/") && !strings.HasSuffix(raw, ".md") {
			continue
		}
		targetText, err := url.PathUnescape(strings.SplitN(raw, "#", 2)[0])
		if err != nil {
			*problems = append(*problems, fmt.Errorf("%s: decode link %q: %w", relative(root, source), raw, err))
			continue
		}
		if targetText == "" {
			continue
		}
		target := filepath.Clean(filepath.Join(filepath.Dir(source), filepath.FromSlash(targetText)))
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			*problems = append(*problems, fmt.Errorf("%s: local link %q escapes the repository", relative(root, source), raw))
			continue
		}
		if _, err := os.Stat(target); err != nil {
			*problems = append(*problems, fmt.Errorf("%s: local link %q does not resolve: %w", relative(root, source), raw, err))
		}
	}
}

func readFrontmatter(path string) (frontmatter, string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return frontmatter{}, "", fmt.Errorf("read: %w", err)
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return frontmatter{}, "", errors.New("missing YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return frontmatter{}, "", errors.New("unterminated YAML frontmatter")
	}
	end += 4
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(text[4:end]), &fm); err != nil {
		return frontmatter{}, "", fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		return frontmatter{}, "", errors.New("frontmatter name is required")
	}
	return fm, text[end+5:], nil
}

func shouldSkip(root, path, name string) bool {
	if path == root {
		return false
	}
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "coverage":
		return true
	}
	rel := filepath.ToSlash(relative(root, path))
	return rel == ".zarlcode/pr-worktrees" || strings.HasPrefix(rel, ".zarlcode/pr-worktrees/") ||
		rel == ".zarlcode/sessions" || strings.HasPrefix(rel, ".zarlcode/sessions/")
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
