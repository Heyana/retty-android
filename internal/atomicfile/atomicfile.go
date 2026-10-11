// Package atomicfile replaces small state files without ever leaving a torn
// one behind: readers see either the old contents or the new, never a mix.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write stores data at path through a synced temporary file renamed into
// place. The file is private to the user (0600), and its directory is
// created (0700) when missing.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
