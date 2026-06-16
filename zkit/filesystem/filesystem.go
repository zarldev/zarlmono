package filesystem

import (
	"io"
	"io/fs"
)

const (
	// ModePrivateDir is for directories that may contain credentials,
	// process state, tokens, or other user-private data.
	ModePrivateDir fs.FileMode = 0o700

	// ModePrivateFile is for files that may contain credentials, tokens,
	// debug logs, or other user-private data.
	ModePrivateFile fs.FileMode = 0o600

	// ModeSharedDir is for directories that may be traversed by the owning
	// user and group, but not by other users on the system.
	ModeSharedDir fs.FileMode = 0o750

	// ModePublicDir is for intentionally user-readable directory trees.
	// Prefer ModePrivateDir or ModeSharedDir unless public traversal is a
	// deliberate part of the file's UX.
	ModePublicDir fs.FileMode = 0o755

	// ModePublicFile is for intentionally user-readable config, source, or
	// documentation files. Do not use for secrets, state databases, or logs.
	ModePublicFile fs.FileMode = 0o644

	// ModeExecutableFile is for generated helper binaries/scripts that must
	// be executable by the owning user and readable/executable by others.
	ModeExecutableFile fs.FileMode = 0o755
)

// ReadFileFS defines the interface for reading files.
type ReadFileFS interface {
	// ReadFile reads the file named by filename and returns the contents.
	ReadFile(filename string) ([]byte, error)
}

// WriteFileFS defines the interface for writing files.
type WriteFileFS interface {
	// WriteFile writes data to a file named by filename.
	WriteFile(filename string, data []byte, perm fs.FileMode) error
}

// RemoveFS defines the interface for removing files.
type RemoveFS interface {
	// Remove removes the named file.
	Remove(filename string) error
}

// MkdirFS defines the interface for creating directories.
type MkdirFS interface {
	// MkdirAll creates a directory named path, along with any necessary parents.
	MkdirAll(path string, perm fs.FileMode) error
}

// File is a file that can be read from and written to.
type File interface {
	fs.File
	io.Writer
}

// OpenFileFS defines the interface for opening files with flags.
type OpenFileFS interface {
	// OpenFile opens the named file with specified flag and perm.
	OpenFile(name string, flag int, perm fs.FileMode) (File, error)
}

// WalkDirFS defines the interface for walking directory trees.
type WalkDirFS interface {
	// WalkDir walks the file tree rooted at root, calling fn for each file or
	// directory in the tree, including root.
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// ReadWriteFileFS defines a complete file system interface that combines
// all basic file operations through interface composition.
type ReadWriteFileFS interface {
	ReadFileFS
	WriteFileFS
	RemoveFS
	MkdirFS
	OpenFileFS
	WalkDirFS
}
