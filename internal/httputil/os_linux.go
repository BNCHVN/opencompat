//go:build linux

package httputil

import (
	"os"
	"strings"
)

// GetOSInfo returns OS name and version for Linux.
// Attempts to read from /etc/os-release.
func GetOSInfo() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux"
	}

	var name, version string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "NAME=") {
			name = strings.Trim(strings.TrimPrefix(line, "NAME="), "\"")
		} else if strings.HasPrefix(line, "VERSION_ID=") {
			version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		}
	}

	if name == "" {
		return "Linux"
	}
	if version != "" {
		return name + " " + version
	}
	return name
}
