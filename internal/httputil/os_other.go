//go:build !darwin && !linux && !windows

package httputil

import "runtime"

// GetOSInfo returns a generic OS identifier for unsupported platforms.
func GetOSInfo() string {
	return runtime.GOOS
}
