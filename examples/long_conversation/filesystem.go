package main

import (
	"fmt"
	"sync"
)

// FileSystem holds verbose source files to trigger compaction.
type FileSystem struct {
	mu    sync.RWMutex
	files map[string]string
}

// NewFileSystem creates a filesystem with deliberately verbose files
// that will trigger context pressure during research.
func NewFileSystem() *FileSystem {
	return &FileSystem{
		files: map[string]string{
			"handlers.go": `package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// GetUserHandler handles GET /users/{id}
func GetUserHandler(w http.ResponseWriter, r *http.Request) {
	id := extractID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing user ID")
		return
	}
	user, err := fetchUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("user %s not found", id))
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// CreateUserHandler handles POST /users
func CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	user, err := createUser(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// UpdateUserHandler handles PUT /users/{id}
func UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	id := extractID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing user ID")
		return
	}
	var req UpdateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := updateUser(r.Context(), id, req)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("user %s not found", id))
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// DeleteUserHandler handles DELETE /users/{id}
func DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	id := extractID(r)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing user ID")
		return
	}
	if err := deleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("user %s not found", id))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListUsersHandler handles GET /users
func ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	users, err := listUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}
`,

			"docs.go": `package main

// Package docs contains comprehensive documentation for the user management API.
//
// This API provides CRUD operations for user resources with the following endpoints:
//
//	GET    /users      - List all users
//	GET    /users/{id} - Get a specific user
//	POST   /users      - Create a new user
//	PUT    /users/{id} - Update an existing user
//	DELETE /users/{id} - Delete a user
//
// # Authentication
//
// All endpoints require a valid API key passed in the Authorization header:
//
//	Authorization: Bearer <api-key>
//
// # Rate Limiting
//
// The API enforces rate limiting per API key:
// - 100 requests per minute for read operations
// - 50 requests per minute for write operations
// - 10 requests per minute for delete operations
//
// # Response Format
//
// All responses are JSON-encoded with the following envelope:
//
//	{
//	  "data": { ... },          // Response payload
//	  "error": null,            // Error message (null on success)
//	  "meta": {                 // Metadata
//	    "request_id": "...",
//	    "duration_ms": 42
//	  }
//	}
//
// # Error Codes
//
// The API returns standard HTTP status codes:
//   - 200: Success
//   - 201: Created
//   - 204: No Content (deletion success)
//   - 400: Bad Request
//   - 401: Unauthorized
//   - 404: Not Found
//   - 422: Unprocessable Entity
//   - 429: Too Many Requests
//   - 500: Internal Server Error
//
// # Pagination
//
// List endpoints support cursor-based pagination:
//
//	GET /users?cursor=abc123&limit=20
//
// The response includes a "next" cursor for the next page.
//
// # Filtering and Sorting
//
// The list endpoint supports:
//   - filter: filter by field values (e.g., ?filter[role]=admin)
//   - sort: sort by a field (e.g., ?sort=created_at)
//   - order: asc or desc
//
// # Schema Definitions
//
// # User Object
//
//	{
//	  "id": "usr_abc123",
//	  "email": "user@example.com",
//	  "name": "John Doe",
//	  "role": "admin",
//	  "status": "active",
//	  "created_at": "2024-01-01T00:00:00Z",
//	  "updated_at": "2024-01-15T12:30:00Z",
//	  "metadata": {
//	    "department": "engineering",
//	    "manager_id": "usr_xyz789"
//	  }
//	}
//
// # CreateUserRequest
//
//	{
//	  "email": "user@example.com",
//	  "name": "John Doe",
//	  "role": "member",
//	  "metadata": {}
//	}
//
// # UpdateUserRequest
//
//	{
//	  "email": "new@example.com",
//	  "name": "New Name",
//	  "role": "admin",
//	  "metadata": {}
//	}
//
// # Data Model
//
// Users are stored with the following columns:
//   - id (string, primary key)
//   - email (string, unique, indexed)
//   - name (string, required)
//   - role (string, enum: admin|member|viewer)
//   - status (string, enum: active|inactive|suspended)
//   - metadata (jsonb)
//   - created_at (timestamp)
//   - updated_at (timestamp)
`,

			"utils.go": `package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// extractID extracts the user ID from the URL path.
func extractID(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// decodeJSON decodes a JSON request body into a struct.
func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		return fmt.Errorf("reading body: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decoding JSON: %w", err)
	}
	return nil
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ValidateEmail validates an email address format.
func ValidateEmail(email string) error {
	if email == "" {
		return errors.New("email is required")
	}
	if !strings.Contains(email, "@") {
		return errors.New("invalid email format")
	}
	return nil
}

// ValidateName validates a name field.
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < 2 {
		return errors.New("name must be at least 2 characters")
	}
	if len(name) > 100 {
		return errors.New("name must be at most 100 characters")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsSpace(r) && r != '-' && r != '\'' {
			return fmt.Errorf("name contains invalid character: %c", r)
		}
	}
	return nil
}

// ValidateRole validates a user role.
func ValidateRole(role string) error {
	switch role {
	case "admin", "member", "viewer":
		return nil
	}
	return fmt.Errorf("invalid role: %s", role)
}

// Stub implementations for compilation
func fetchUser(ctx context.Context, id string) (map[string]interface{}, error) {
	return nil, errors.New("not implemented") 
}
func createUser(ctx context.Context, req interface{}) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}
func updateUser(ctx context.Context, id string, req interface{}) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}
func deleteUser(ctx context.Context, id string) error {
	return errors.New("not implemented") 
}
func listUsers(ctx context.Context) ([]map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}

type CreateUserRequest struct {
	Email    string
	Name     string
	Role     string
	Metadata map[string]interface{}
}
func (r CreateUserRequest) Validate() error {
	if err := ValidateEmail(r.Email); err != nil { return err }
	if err := ValidateName(r.Name); err != nil { return err }
	if err := ValidateRole(r.Role); err != nil { return err }
	return nil
}

type UpdateUserRequest struct {
	Email    string
	Name     string
	Role     string
	Metadata map[string]interface{}
}
func (r UpdateUserRequest) Validate() error {
	if r.Email != "" {
		if err := ValidateEmail(r.Email); err != nil { return err }
	}
	if r.Name != "" {
		if err := ValidateName(r.Name); err != nil { return err }
	}
	if r.Role != "" {
		if err := ValidateRole(r.Role); err != nil { return err }
	}
	return nil
}
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

// ResearchContext tracks the accumulated research findings.
type ResearchContext struct {
	mu          sync.Mutex
	filesRead   []string
	totalLines  int
	functions   []string
	compactions int
}

// NewResearchContext creates a new research context.
func NewResearchContext() *ResearchContext {
	return &ResearchContext{}
}

// RecordFile tracks that a file was read.
func (rc *ResearchContext) RecordFile(path string, lines int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.filesRead = append(rc.filesRead, path)
	rc.totalLines += lines
}

// RecordFunction tracks a discovered function.
func (rc *ResearchContext) RecordFunction(name string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.functions = append(rc.functions, name)
}

// RecordCompaction increments the compaction counter.
func (rc *ResearchContext) RecordCompaction() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.compactions++
}

// Summary returns a research progress summary.
func (rc *ResearchContext) Summary() string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return fmt.Sprintf("files=%d lines=%d functions=%d compactions=%d",
		len(rc.filesRead), rc.totalLines, len(rc.functions), rc.compactions)
}

// ListFunctions returns all discovered functions.
func (rc *ResearchContext) ListFunctions() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]string, len(rc.functions))
	copy(out, rc.functions)
	return out
}
