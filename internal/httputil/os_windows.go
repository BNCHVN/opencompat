//go:build windows

package httputil

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// GetOSInfo returns OS name and version for Windows.
func GetOSInfo() string {
	version := windows.RtlGetVersion()
	return fmt.Sprintf("Windows %d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
}
