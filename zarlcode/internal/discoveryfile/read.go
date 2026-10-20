// Package discoveryfile reads bounded, regular-file discovery inputs.
package discoveryfile

import (
	"fmt"
	"io"
)

// Read follows file symlinks and reads at most limit bytes from a regular file.
// Oversized inputs are rejected, never parsed or executed as partial definitions.
// On Unix the open is nonblocking, so a FIFO substituted before open cannot
// strand discovery. Descriptor validation also rejects other special files.
func Read(path string, limit int64) ([]byte, error) {
	f, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("open discovery file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat discovery file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("discovery file %q is not a regular file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("discovery file %q exceeds %d bytes", path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read discovery file %q: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("discovery file %q exceeds %d bytes", path, limit)
	}
	return data, nil
}
