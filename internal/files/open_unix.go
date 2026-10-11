//go:build darwin || linux || ios

package files

import (
	"os"
	"syscall"
)

func openRead(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
