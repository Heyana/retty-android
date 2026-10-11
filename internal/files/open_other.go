//go:build !darwin && !linux && !ios

package files

import (
	"errors"
	"os"
)

func openRead(path string) (*os.File, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("仅支持读取普通文件")
	}
	return os.Open(path)
}
