package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileSystem is the "world" for this example — an in-memory filesystem
// that tracks created/modified files. The oracle verifies outcomes by
// checking this state, not by trusting model claims.
type FileSystem struct {
	mu      sync.RWMutex
	files   map[string]string // path → content
	basedir string
}

// NewFileSystem creates a new filesystem with some initial auth code.
func NewFileSystem(basedir string) *FileSystem {
	fs := &FileSystem{
		files:   make(map[string]string),
		basedir: basedir,
	}
	// Seed with initial auth implementation
	fs.files["auth.go"] = `package auth

import "net/http"

// SessionAuth handles cookie-based session authentication
type SessionAuth struct {
	sessions map[string]string
}

func (a *SessionAuth) Authenticate(r *http.Request) (string, error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", err
	}
	user, ok := a.sessions[cookie.Value]
	if !ok {
		return "", fmt.Errorf("invalid session")
	}
	return user, nil
}
`
	fs.files["session.go"] = `package auth

import "time"

type Session struct {
	User      string
	CreatedAt time.Time
}

var sessions = make(map[string]Session)

func CreateSession(user string) string {
	id := generateID()
	sessions[id] = Session{User: user, CreatedAt: time.Now()}
	return id
}
`
	return fs
}

// Read returns the content of a file, or ("", false) if not found.
func (fs *FileSystem) Read(path string) (string, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	content, ok := fs.files[path]
	return content, ok
}

// Write creates or overwrites a file.
func (fs *FileSystem) Write(path, content string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.files[path] = content
}

// List returns all files in the filesystem.
func (fs *FileSystem) List() []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	var out []string
	for name := range fs.files {
		out = append(out, name)
	}
	return out
}

// ModifiedSince returns files created or modified after the given checkpoint.
func (fs *FileSystem) ModifiedSince(checkpoint map[string]string) []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	var modified []string
	for name, content := range fs.files {
		old, existed := checkpoint[name]
		if !existed {
			modified = append(modified, name+" (created)")
		} else if old != content {
			modified = append(modified, name+" (modified)")
		}
	}
	return modified
}

// Checkpoint captures the current state for comparison.
func (fs *FileSystem) Checkpoint() map[string]string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	out := make(map[string]string, len(fs.files))
	for k, v := range fs.files {
		out[k] = v
	}
	return out
}

// HasFile reports whether a file exists.
func (fs *FileSystem) HasFile(path string) bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	_, ok := fs.files[path]
	return ok
}

// RefactorComplete checks if the JWT refactor appears complete.
func (fs *FileSystem) RefactorComplete() bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	// Look for JWT-related files and content
	hasJWT := false
	modifiedAuth := false
	for name, content := range fs.files {
		if name == "jwt.go" {
			hasJWT = true
		}
		if name == "auth.go" && strings.Contains(content, "JWT") {
			modifiedAuth = true
		}
	}
	return hasJWT && modifiedAuth
}

// Summary returns a human-readable state summary.
func (fs *FileSystem) Summary() string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	var parts []string
	for name, content := range fs.files {
		lines := strings.Count(content, "\n")
		parts = append(parts, fmt.Sprintf("%s (%d lines)", name, lines))
	}
	return strings.Join(parts, ", ")
}

// WriteToDisk writes all files to the real filesystem (for verification).
func (fs *FileSystem) WriteToDisk() error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if err := os.MkdirAll(fs.basedir, 0755); err != nil {
		return err
	}
	for name, content := range fs.files {
		path := filepath.Join(fs.basedir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

// Reset clears the filesystem back to initial state.
func (fs *FileSystem) Reset() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.files = make(map[string]string)
	// Re-seed
	fs.files["auth.go"] = `package auth

import "net/http"

// SessionAuth handles cookie-based session authentication
type SessionAuth struct {
	sessions map[string]string
}

func (a *SessionAuth) Authenticate(r *http.Request) (string, error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", err
	}
	user, ok := a.sessions[cookie.Value]
	if !ok {
		return "", fmt.Errorf("invalid session")
	}
	return user, nil
}
`
	fs.files["session.go"] = `package auth

import "time"

type Session struct {
	User      string
	CreatedAt time.Time
}

var sessions = make(map[string]Session)

func CreateSession(user string) string {
	id := generateID()
	sessions[id] = Session{User: user, CreatedAt: time.Now()}
	return id
}
`
}
