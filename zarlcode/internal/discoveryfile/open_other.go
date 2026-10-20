//go:build !unix

package discoveryfile

import (
	"fmt"
	"os"
)

func open(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("discovery file %q is not a regular file", path)
	}
	return os.Open(path)
}

func openDirectory(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("discovery directory %q is not a directory", path)
	}
	return os.Open(path)
}
