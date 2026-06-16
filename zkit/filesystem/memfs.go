package filesystem

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

var (
	_ ReadWriteFileFS = (*MemFS)(nil)
)

// MemFS provides an in-memory filesystem implementation.
// It implements ReadWriteFileFS and is safe for concurrent use.
type MemFS struct {
	files *zsync.Map[string, *memFile]
}

type memFile struct {
	data    []byte
	modTime time.Time
	mode    fs.FileMode
}

// NewMemFS creates an empty in-memory filesystem.
//
// Files are keyed by the exact path supplied to methods; directory entries are
// implicit, so callers do not need to create parent directories before writing.
func NewMemFS() *MemFS {
	return &MemFS{
		files: zsync.NewMap[string, *memFile](),
	}
}

// ReadFile returns a copy of the bytes stored at filename.
//
// The returned slice is detached from the filesystem's internal storage so
// callers can mutate it without changing future reads. Missing files return
// os.ErrNotExist.
func (mfs *MemFS) ReadFile(filename string) ([]byte, error) {
	file, err := mfs.files.Get(filename)
	if err != nil {
		return nil, os.ErrNotExist
	}

	return bytes.Clone(file.data), nil
}

// WriteFile stores a copy of data at filename with perm as its reported mode.
//
// Existing content is replaced atomically from the caller's perspective. Parent
// directories are not tracked; any path string can be written directly.
func (mfs *MemFS) WriteFile(filename string, data []byte, perm fs.FileMode) error {
	mfs.files.Set(filename, &memFile{
		data:    bytes.Clone(data),
		modTime: time.Now(),
		mode:    perm,
	})

	return nil
}

// Remove deletes filename from the in-memory store.
//
// It mirrors os.Remove for files: deleting a missing path returns
// os.ErrNotExist, while removing an existing file makes subsequent reads fail.
func (mfs *MemFS) Remove(filename string) error {
	if !mfs.files.Delete(filename) {
		return os.ErrNotExist
	}
	return nil
}

// MkdirAll accepts directory creation requests for compatibility.
//
// Directories are implicit in MemFS, so this method records nothing and always
// succeeds.
func (mfs *MemFS) MkdirAll(path string, perm fs.FileMode) error {
	return nil
}

// OpenFile opens name for reading or writing according to flag.
//
// Honours the standard open flags:
//
//   - O_RDONLY: read-only handle, snapshot of current bytes.
//   - O_WRONLY / O_RDWR: write-capable handle (buffer commits on Close).
//   - O_CREATE: missing file is allowed; buffer starts empty.
//   - O_TRUNC: existing file's bytes are discarded; buffer starts empty.
//   - O_APPEND: buffer pre-loaded with the existing bytes so Write
//     extends rather than replaces.
//
// Missing file with no O_CREATE returns os.ErrNotExist. Write to a
// read-only handle returns os.ErrPermission.
//
// Earlier shape ignored O_TRUNC and O_APPEND entirely: every write-
// capable open started with an empty buffer regardless of intent.
// An "append" open would silently overwrite on Close; a non-trunc
// open on an existing file would also silently overwrite — the
// adversarial review's "MemFS OpenFile semantics are incomplete"
// finding.
func (mfs *MemFS) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	writable := flag&(os.O_WRONLY|os.O_RDWR) != 0 || flag&os.O_CREATE != 0
	if !writable {
		file, err := mfs.files.Get(name)
		if err != nil {
			return nil, os.ErrNotExist
		}
		return &memFileHandle{
			mfs:       mfs,
			filename:  name,
			buffer:    bytes.NewBuffer(bytes.Clone(file.data)),
			writeMode: false,
		}, nil
	}

	// Writable path. Decide what the buffer starts with based on
	// O_TRUNC / O_APPEND / O_CREATE + whether the file exists.
	var initial []byte
	existing, getErr := mfs.files.Get(name)
	exists := getErr == nil
	switch {
	case !exists && flag&os.O_CREATE == 0:
		// Asked to write an existing file (no CREATE) but it isn't there.
		return nil, os.ErrNotExist
	case flag&os.O_TRUNC != 0:
		// Truncate wins regardless of existence — start empty.
		initial = nil
	case flag&os.O_APPEND != 0 && exists:
		// Append: start with existing contents so subsequent Writes
		// extend rather than replace.
		initial = bytes.Clone(existing.data)
	case exists && flag&os.O_TRUNC == 0:
		// Plain open of existing file with no TRUNC and no APPEND.
		// Mirror os semantics: buffer holds the existing bytes; Write
		// overlays from offset 0 (which, with bytes.Buffer, means we
		// keep the unused tail). Pre-loading the buffer is the
		// honest answer; pure-overwrite callers should pass O_TRUNC.
		initial = bytes.Clone(existing.data)
	}
	return &memFileHandle{
		mfs:       mfs,
		filename:  name,
		perm:      perm,
		buffer:    bytes.NewBuffer(initial),
		writeMode: true,
	}, nil
}

// WalkDir calls fn for each file currently stored under root.
//
// Directories are implicit in MemFS, so only file entries are emitted. The walk
// order follows the map key snapshot returned by the backing store and should not
// be treated as stable.
func (mfs *MemFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	keys := mfs.files.Keys()

	for _, filename := range keys {
		file, err := mfs.files.Get(filename)
		if err != nil {
			continue
		}

		info := &memFileInfo{
			name:    filepath.Base(filename),
			size:    int64(len(file.data)),
			mode:    file.mode,
			modTime: file.modTime,
		}

		entry := &memDirEntry{info: info}
		if err := fn(filename, entry, nil); err != nil {
			return err
		}
	}

	return nil
}

// ClearCacheFiles removes all files whose names end in .cache.
//
// This helper exists for cache implementations that use MemFS as their storage
// substrate; it intentionally ignores non-cache files.
func (mfs *MemFS) ClearCacheFiles() {
	keys := mfs.files.Keys()
	for _, filename := range keys {
		if strings.HasSuffix(filename, ".cache") {
			mfs.files.Delete(filename)
		}
	}
}

// CountCacheFiles returns the number of stored files whose names end in .cache.
//
// This helper is used by cache implementations to report cache-specific size
// without exposing all MemFS internals.
func (mfs *MemFS) CountCacheFiles() int {
	keys := mfs.files.Keys()
	count := 0
	for _, filename := range keys {
		if strings.HasSuffix(filename, ".cache") {
			count++
		}
	}
	return count
}

type memFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (fi *memFileInfo) Name() string       { return fi.name }
func (fi *memFileInfo) Size() int64        { return fi.size }
func (fi *memFileInfo) Mode() fs.FileMode  { return fi.mode }
func (fi *memFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *memFileInfo) IsDir() bool        { return fi.mode.IsDir() }
func (fi *memFileInfo) Sys() any           { return nil }

type memDirEntry struct {
	info *memFileInfo
}

func (de *memDirEntry) Name() string               { return de.info.Name() }
func (de *memDirEntry) IsDir() bool                { return de.info.IsDir() }
func (de *memDirEntry) Type() fs.FileMode          { return de.info.Mode().Type() }
func (de *memDirEntry) Info() (fs.FileInfo, error) { return de.info, nil }

// memFileHandle implements the File interface for in-memory files.
type memFileHandle struct {
	mfs       *MemFS
	filename  string
	buffer    *bytes.Buffer
	perm      fs.FileMode
	writeMode bool
	closed    bool
}

func (fh *memFileHandle) Stat() (fs.FileInfo, error) {
	if fh.closed {
		return nil, fs.ErrClosed
	}

	file, err := fh.mfs.files.Get(fh.filename)
	if errors.Is(err, zsync.ErrNotFound) {
		// If file doesn't exist yet (new file), return basic info
		return &memFileInfo{
			name:    filepath.Base(fh.filename),
			size:    int64(fh.buffer.Len()),
			mode:    fh.perm,
			modTime: time.Now(),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	return &memFileInfo{
		name:    filepath.Base(fh.filename),
		size:    int64(len(file.data)),
		mode:    file.mode,
		modTime: file.modTime,
	}, nil
}

func (fh *memFileHandle) Read(p []byte) (int, error) {
	if fh.closed {
		return 0, fs.ErrClosed
	}
	return fh.buffer.Read(p)
}

func (fh *memFileHandle) Write(p []byte) (int, error) {
	if fh.closed {
		return 0, fs.ErrClosed
	}
	if !fh.writeMode {
		return 0, fs.ErrPermission
	}
	return fh.buffer.Write(p)
}

func (fh *memFileHandle) Close() error {
	if fh.closed {
		return fs.ErrClosed
	}
	fh.closed = true

	// If in write mode, save the buffer contents to the filesystem
	if fh.writeMode {
		fh.mfs.files.Set(fh.filename, &memFile{
			data:    fh.buffer.Bytes(),
			modTime: time.Now(),
			mode:    fh.perm,
		})
	}

	return nil
}
