//go:build android

package main

// The Android host owns the process: the Java side loads libmygo.so, whose
// //export functions drive package mygo's backend, and there is no main() to
// run. jni.Start is the hook an app registers from its package init, which
// runs when the library loads; the host calls the registered function as
// soon as its first surface callback arrives.
//
// Same entry mobileMain takes on iOS: a 390×844 window, since the window's
// size is only a hint on a phone and the surface decides.

import (
	"github.com/egoist/mygo/android/jni"
)

func init() {
	jni.Start(mobileMain)
}
