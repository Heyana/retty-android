package rex

import "golang.org/x/sys/unix"

// SystemShuttingDown reads the kernel's power-off/restart flag without starting
// a native UI runtime. Agent hooks and the headless daemon need it too.
// https://github.com/apple-oss-distributions/xnu/blob/main/iokit/Kernel/IOPMrootDomain.cpp
func SystemShuttingDown() bool {
	value, err := unix.SysctlUint32("kern.willshutdown")
	return err == nil && value != 0
}
