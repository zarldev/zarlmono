package main

import (
	"fmt"
	"strings"
	"sync"
)

// FileSystem is the "world" - a simple in-memory filesystem with Go code.
type FileSystem struct {
	mu    sync.RWMutex
	files map[string]string
}

// NewFileSystem creates a filesystem with some Go source files.
func NewFileSystem() *FileSystem {
	return &FileSystem{
		files: map[string]string{
			"main.go": `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}

func ExistingHandler() {
	// This function exists
}
`,
			"handlers.go": `package main

import "net/http"

func HTTPHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK"))
}

func AnotherHandler() string {
	return "another"
}
`,
			"utils.go": `package main

func Helper() {}
func AnotherHelper() {}
`,
		},
	}
}

// Read returns the content of a file.
func (fs *FileSystem) Read(path string) (string, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	content, ok := fs.files[path]
	return content, ok
}

// List returns all files.
func (fs *FileSystem) List() []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	var names []string
	for name := range fs.files {
		names = append(names, name)
	}
	return names
}

// Grep searches for a pattern in all files.
// Returns matches as "file:line: content" strings.
func (fs *FileSystem) Grep(pattern string) []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var matches []string
	for name, content := range fs.files {
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.Contains(line, pattern) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", name, i+1, line))
			}
		}
	}
	return matches
}

// HasFunction checks if a function with the given name exists.
func (fs *FileSystem) HasFunction(name string) bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	searchPattern := "func " + name + "("
	for _, content := range fs.files {
		if strings.Contains(content, searchPattern) {
			return true
		}
	}
	return false
}

// SearchAttempts tracks how many searches have been performed.
type SearchAttempts struct {
	mu       sync.Mutex
	attempts int
	patterns []string
}

// NewSearchAttempts creates a new attempt tracker.
func NewSearchAttempts() *SearchAttempts {
	return &SearchAttempts{patterns: []string{}}
}

// Record records a search attempt.
func (sa *SearchAttempts) Record(pattern string) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	sa.attempts++
	sa.patterns = append(sa.patterns, pattern)
}

// Count returns the total number of attempts.
func (sa *SearchAttempts) Count() int {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.attempts
}

// Patterns returns all attempted patterns.
func (sa *SearchAttempts) Patterns() []string {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	out := make([]string, len(sa.patterns))
	copy(out, sa.patterns)
	return out
}
