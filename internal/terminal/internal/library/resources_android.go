//go:build !retty_cli && android

package library

import "github.com/egoist/mygo"

// resourceDirs is where a packaged app looks for the libraries it ships.
//
// On Android the only directory an app can put a .so in is the one the
// package manager unpacked it to: lib/<abi>/ inside the APK becomes
// <...>/lib/arm64/ on disk, and that path is the process's
// nativeLibraryDir. There is no PathResources on a phone — os.Executable
// is the app_process binary, and the resources directory the other
// platforms use does not exist.
//
// androidNativeDir finds it from the one library this process is certain
// to have loaded: libmygo.so, the Go runtime itself.
func resourceDirs() []string {
	dirs := make([]string, 0, 2)
	if d := androidNativeDir(); d != "" {
		dirs = append(dirs, d)
	}
	// The resources directory still comes second: a mygo build that copies
	// files there should win only if nativeLibraryDir had nothing.
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		dirs = append(dirs, dir)
	}
	return dirs
}

func packaged() bool { return mygo.App.IsPackaged() }
