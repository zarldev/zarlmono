package filesystem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

var (
	_ ReadWriteFileFS = (*SeaweedFSFileSystem)(nil)
)

// SeaweedFS response-size caps. A malicious or misbehaving filer can
// otherwise return arbitrarily large bodies to ReadFile / WalkDir and
// force the client to allocate without bound — the original shape
// called io.ReadAll on resp.Body unconditionally.
//
// File payloads sit at 64 MiB — enough for typical blob/image
// content; callers needing larger objects should use the streaming
// File interface (Read in chunks via OpenFile) rather than the
// whole-buffer ReadFile path.
//
// Directory listings sit much tighter at 4 MiB — JSON metadata is
// orders of magnitude smaller than payload data, so a multi-MiB
// listing is a misconfiguration / attack signal worth failing on.
const (
	seaweedReadFileCapBytes  = 64 * 1024 * 1024
	seaweedListBodyCapBytes  = 4 * 1024 * 1024
	seaweedErrorBodyCapBytes = 4 * 1024 // for non-2xx error messages
)

// ErrSeaweedResponseTooLarge is returned when a SeaweedFS response
// body exceeds the configured cap. Surfaced as a sentinel so callers
// can switch on it (typical handling: log and either back off or
// switch to the streaming path).
var ErrSeaweedResponseTooLarge = errors.New("seaweedfs: response exceeded size cap")

// SeaweedFSDirectoryEntry represents one entry returned by the SeaweedFS filer
// directory-listing API.
type SeaweedFSDirectoryEntry struct {
	FullPath string `json:"FullPath"`
	Mode     int    `json:"Mode"`
	FileSize int64  `json:"FileSize"`
}

// SeaweedFSDirectoryListing represents the JSON envelope returned by the
// SeaweedFS filer directory-listing API.
type SeaweedFSDirectoryListing struct {
	Path    string                    `json:"Path"`
	Entries []SeaweedFSDirectoryEntry `json:"Entries"`
}

// SeaweedFSFileSystem implements ReadWriteFileFS against a SeaweedFS filer HTTP
// endpoint.
//
// Paths are rooted at basePath. SeaweedFS creates directories implicitly during
// writes, so MkdirAll is a compatibility no-op.
type SeaweedFSFileSystem struct {
	filerURL string
	basePath string
	client   *http.Client
}

// NewSeaweedFSFileSystem creates a SeaweedFS-backed filesystem rooted at basePath.
//
// filerURL should point at the SeaweedFS filer HTTP endpoint. Leading/trailing
// slashes are normalized so callers can pass either raw paths or URL-like path
// segments.
func NewSeaweedFSFileSystem(filerURL, basePath string) *SeaweedFSFileSystem {
	return &SeaweedFSFileSystem{
		filerURL: strings.TrimSuffix(filerURL, "/"),
		basePath: strings.Trim(basePath, "/"),
		// Transport-level dial / TLS / response-header / idle timeouts
		// from [zhttp.DefaultTransport]; no whole-request Timeout
		// because [http.Client.Timeout] covers reading the response
		// body, and a large WriteFile or WalkDir against a slow filer
		// would otherwise be cut off mid-transfer. Lifetime is
		// bounded by the caller's ctx.
		client: &http.Client{Transport: zhttp.DefaultTransport()},
	}
}

// buildURL composes a filer URL for the given relative path. Each
// path segment is URL-escaped so spaces, "?", "#", and other
// reserved characters in user-supplied filenames don't reshape the
// request URL.
//
// Earlier shape concatenated `s.filerURL + s.getFullPath(name)` —
// a filename of "foo?evil=1" would be interpreted as a query string
// by the filer, and a filename of "..%2Fetc%2Fpasswd" decoded back
// into ".." traversal on the server side. The new shape uses
// url.URL.JoinPath which escapes each segment individually.
func (s *SeaweedFSFileSystem) buildURL(name string) (string, error) {
	base, err := url.Parse(s.filerURL)
	if err != nil {
		return "", fmt.Errorf("parse filer url %q: %w", s.filerURL, err)
	}
	segments := []string{}
	if s.basePath != "" {
		segments = append(segments, splitPath(s.basePath)...)
	}
	segments = append(segments, splitPath(name)...)
	joined := base.JoinPath(segments...)
	return joined.String(), nil
}

// splitPath returns the non-empty / non-".." components of a path.
// "../" segments are dropped so they can't escape the basePath on
// the wire; the server is the ultimate authority but stripping
// here gives us a defence-in-depth layer.
func splitPath(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		out = append(out, part)
	}
	return out
}

// readCapped reads up to cap bytes from r and returns
// [ErrSeaweedResponseTooLarge] if r has more bytes to offer. Used
// by every body-reading path to bound parent-process allocation.
func readCapped(r io.Reader, maxSize int64) ([]byte, error) {
	// Read one byte more than the max size; if we got it, the body
	// exceeds the max size and we report the overflow rather than return
	// truncated data the caller would treat as authoritative.
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("%w (cap %d)", ErrSeaweedResponseTooLarge, maxSize)
	}
	return data, nil
}

// readErrorBody pulls a small excerpt of a non-2xx response body so
// the surfaced error mentions what the filer actually said without
// the caller burning megabytes on a malicious response.
func readErrorBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, seaweedErrorBodyCapBytes))
	return strings.TrimSpace(string(b))
}

// ReadFile fetches filename from the SeaweedFS filer and returns its
// contents.
//
// Non-200 responses are returned as errors; a 404 is reported as a
// file-not-found error naming the requested path. The response body
// is capped at [seaweedReadFileCapBytes] to bound client allocation
// against a misbehaving filer.
func (s *SeaweedFSFileSystem) ReadFile(filename string) ([]byte, error) {
	getURL, err := s.buildURL(filename)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create get request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("file not found: %s", filename)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get file: status %d: %s",
			resp.StatusCode, readErrorBody(resp.Body))
	}

	return readCapped(resp.Body, seaweedReadFileCapBytes)
}

// WriteFile uploads data to filename through the SeaweedFS filer.
//
// The perm argument is accepted to satisfy ReadWriteFileFS but
// SeaweedFS stores mode information independently; this method
// primarily controls bytes and content type.
func (s *SeaweedFSFileSystem) WriteFile(filename string, data []byte, perm fs.FileMode) error {
	uploadURL, err := s.buildURL(filename)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Set content type based on file extension
	req.Header.Set("Content-Type", getContentTypeFromFilename(filename))
	req.ContentLength = int64(len(data))

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload: status %d: %s",
			resp.StatusCode, readErrorBody(resp.Body))
	}

	return nil
}

// Remove deletes filename from SeaweedFS.
//
// Missing files are treated as already removed and therefore do not
// return an error.
func (s *SeaweedFSFileSystem) Remove(filename string) error {
	deleteURL, err := s.buildURL(filename)
	if err != nil {
		return fmt.Errorf("create delete request: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, deleteURL, nil)
	if err != nil {
		return fmt.Errorf("create delete request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete: status %d: %s",
			resp.StatusCode, readErrorBody(resp.Body))
	}

	return nil
}

// MkdirAll accepts directory creation requests for compatibility.
//
// SeaweedFS creates parent directories implicitly when files are uploaded, so no
// HTTP request is required here.
func (s *SeaweedFSFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	// SeaweedFS creates directories implicitly when files are uploaded
	// No explicit mkdir operation needed
	return nil
}

// OpenFile returns a buffered file handle for SeaweedFS.
//
// Reads lazily fetch the remote object on first Read. Writes are buffered locally
// and uploaded on Close, matching the File interface without holding an HTTP
// request open across writes.
func (s *SeaweedFSFileSystem) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	return &seaweedFSFile{
		fs:       s,
		filename: name,
		flag:     flag,
		perm:     perm,
	}, nil
}

// WalkDir lists root through the SeaweedFS filer and calls fn for
// each returned entry.
//
// The root entry is emitted first. If the remote directory is
// missing, fn is called once with fs.ErrNotExist so callers see the
// same shape as fs.WalkDir error callbacks.
//
// The listing body is capped at [seaweedListBodyCapBytes] —
// directory listings are JSON metadata, several MiB is already a
// misconfiguration signal worth failing loudly on.
func (s *SeaweedFSFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	listURL, err := s.buildURL(root)
	if err != nil {
		return fmt.Errorf("create list request: %w", err)
	}
	// SeaweedFS filer wants a trailing slash to indicate "list this
	// directory" rather than "fetch this object".
	if !strings.HasSuffix(listURL, "/") {
		listURL += "/"
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, listURL, nil)
	if err != nil {
		return fmt.Errorf("create list request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Directory doesn't exist, call fn with root as not found
		return fn(root, &seaweedFSNotFoundDirEntry{name: root}, fs.ErrNotExist)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("list: status %d: %s",
			resp.StatusCode, readErrorBody(resp.Body))
	}

	body, err := readCapped(resp.Body, seaweedListBodyCapBytes)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	var listing SeaweedFSDirectoryListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return fmt.Errorf("parse JSON response: %w", err)
	}

	// Call fn for the root directory first
	rootEntry := &seaweedFSDirEntry{
		name:  root,
		isDir: true,
		size:  0,
		mode:  fs.ModeDir | 0755,
	}
	if err := fn(root, rootEntry, nil); err != nil {
		return err
	}

	// Process all entries
	for _, entry := range listing.Entries {
		if entry.FullPath == "" {
			continue
		}

		// Remove base path prefix to get relative path
		relPath := strings.TrimPrefix(entry.FullPath, "/"+s.basePath+"/")
		if relPath == entry.FullPath {
			relPath = strings.TrimPrefix(entry.FullPath, "/")
		}

		// Create DirEntry
		dirEntry := &seaweedFSDirEntry{
			name:  filepath.Base(relPath),
			isDir: entry.Mode != 432, // Mode 432 = regular file
			size:  entry.FileSize,
			mode:  fileModeFromPermissionBits(entry.Mode),
		}

		if err := fn(relPath, dirEntry, nil); err != nil {
			return err
		}
	}

	return nil
}

// seaweedFSFile implements File interface for SeaweedFS.
type seaweedFSFile struct {
	fs       *SeaweedFSFileSystem
	filename string
	flag     int
	perm     fs.FileMode
	buffer   *bytes.Buffer
	closed   bool
}

func (f *seaweedFSFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}

	if f.buffer == nil {
		// Read file content from SeaweedFS
		data, err := f.fs.ReadFile(f.filename)
		if err != nil {
			return 0, err
		}
		f.buffer = bytes.NewBuffer(data)
	}

	return f.buffer.Read(p)
}

func (f *seaweedFSFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}

	if f.buffer == nil {
		f.buffer = &bytes.Buffer{}
	}

	return f.buffer.Write(p)
}

func (f *seaweedFSFile) Close() error {
	if f.closed {
		return nil
	}

	f.closed = true

	// If we have a buffer and it's been written to, upload to SeaweedFS
	if f.buffer != nil && f.buffer.Len() > 0 {
		return f.fs.WriteFile(f.filename, f.buffer.Bytes(), f.perm)
	}

	return nil
}

func (f *seaweedFSFile) Stat() (fs.FileInfo, error) {
	if f.closed {
		return nil, fs.ErrClosed
	}

	// f.buffer is lazily allocated on first Read/Write — guard the
	// Len() call so a Stat before any IO doesn't nil-deref. Earlier
	// shape would panic in this position; a default size of 0 is the
	// honest answer for "we haven't touched the file yet".
	var size int64
	if f.buffer != nil {
		size = int64(f.buffer.Len())
	}
	return &seaweedFSFileInfo{
		name: filepath.Base(f.filename),
		size: size,
		mode: f.perm,
	}, nil
}

// seaweedFSDirEntry implements fs.DirEntry.
type seaweedFSDirEntry struct {
	name  string
	isDir bool
	size  int64
	mode  fs.FileMode
}

func (d *seaweedFSDirEntry) Name() string {
	return d.name
}

func (d *seaweedFSDirEntry) IsDir() bool {
	return d.isDir
}

func (d *seaweedFSDirEntry) Type() fs.FileMode {
	if d.isDir {
		return fs.ModeDir
	}
	return 0
}

func (d *seaweedFSDirEntry) Info() (fs.FileInfo, error) {
	return &seaweedFSFileInfo{
		name: d.name,
		size: d.size,
		mode: d.mode,
	}, nil
}

// seaweedFSNotFoundDirEntry represents a not found directory entry.
type seaweedFSNotFoundDirEntry struct {
	name string
}

func (d *seaweedFSNotFoundDirEntry) Name() string {
	return d.name
}

func (d *seaweedFSNotFoundDirEntry) IsDir() bool {
	return true
}

func (d *seaweedFSNotFoundDirEntry) Type() fs.FileMode {
	return fs.ModeDir
}

func (d *seaweedFSNotFoundDirEntry) Info() (fs.FileInfo, error) {
	return nil, fs.ErrNotExist
}

// seaweedFSFileInfo implements fs.FileInfo.
type seaweedFSFileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (f *seaweedFSFileInfo) Name() string {
	return f.name
}

func (f *seaweedFSFileInfo) Size() int64 {
	return f.size
}

func (f *seaweedFSFileInfo) Mode() fs.FileMode {
	return f.mode
}

func (f *seaweedFSFileInfo) ModTime() time.Time {
	return time.Now() // SeaweedFS doesn't provide modification time in basic listing
}

func (f *seaweedFSFileInfo) IsDir() bool {
	return f.mode.IsDir()
}

func (f *seaweedFSFileInfo) Sys() any {
	return nil
}

// Helper function to determine content type from filename.
func getContentTypeFromFilename(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))

	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	default:
		return "application/octet-stream"
	}
}
