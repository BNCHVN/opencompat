//go:build darwin

package httputil

import (
	"strings"
	"syscall"
)

// GetOSInfo returns OS name and version for macOS.
// Format: "Mac OS {version}" (e.g., "Mac OS 14.0.0")
func GetOSInfo() string {
	version, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return "Mac OS"
	}
	// Trim null terminator if present
	version = strings.TrimRight(version, "\x00")
	version = strings.TrimSpace(version)
	if version == "" {
		return "Mac OS"
	}
	return "Mac OS " + version
}
