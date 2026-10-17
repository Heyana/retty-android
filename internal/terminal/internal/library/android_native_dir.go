//go:build !retty_cli && android

package library

import (
	"os"
	"path/filepath"
	"strings"
)

// androidNativeDir returns the directory the package manager unpacked this
// app's native libraries into — nativeLibraryDir.
//
// 🔴 It is found from /proc/self/maps rather than from an API, because there
// is no Go API for it on Android and os.Executable is the app_process binary
// (the container every app runs in), not the app. The process has already
// mapped libmygo.so, this package's own library, so its line in maps names
// the directory outright:
//
//	7c3a1e0000-7c3a2e0000 r-xp 00000000 fd:01 1234 /data/app/.../lib/arm64/libmygo.so
//
// It returns "" when nothing matches, and the caller falls back to the
// resources directory, which is what a device without a packaged library
// would need anyway.
func androidNativeDir() string {
	data, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		// The path is everything after the inode column; a mapped file's
		// name may contain spaces (a debuggable build under /data/local),
		// so the cut is by " /" and the rest is kept whole.
		i := strings.LastIndex(line, " /")
		if i < 0 {
			continue
		}
		path := line[i+1:]
		if filepath.Base(path) != "libmygo.so" {
			continue
		}
		return filepath.Dir(path)
	}
	return ""
}
