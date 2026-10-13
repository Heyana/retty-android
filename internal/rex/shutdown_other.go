//go:build !darwin

package rex

// SystemShuttingDown has no power-off detector on these platforms yet.
func SystemShuttingDown() bool { return false }
