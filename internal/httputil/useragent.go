// Package httputil provides HTTP client utilities.
package httputil

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// BuildUserAgent constructs a User-Agent string in the format:
// {product}/{version} ({OS} {version}; {arch}) {terminal}
func BuildUserAgent(product, version string) string {
	osInfo := GetOSInfo()
	arch := GetArchitecture()
	terminal := GetTerminalInfo()
	return fmt.Sprintf("%s/%s (%s; %s) %s",
		product, version, osInfo, arch, terminal)
}

// GetArchitecture returns the architecture string.
func GetArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

// GetTerminalInfo detects the terminal from environment variables.
func GetTerminalInfo() string {
	program := os.Getenv("TERM_PROGRAM")
	version := os.Getenv("TERM_PROGRAM_VERSION")
	term := os.Getenv("TERM")

	var result string
	if program != "" {
		if version != "" {
			result = program + "/" + version
		} else {
			result = program
		}
	} else if term != "" {
		result = term
	} else {
		result = "unknown"
	}
	return SanitizeHeaderValue(result)
}

// SanitizeHeaderValue removes invalid header characters, replacing them with underscores.
func SanitizeHeaderValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '/' {
			b.WriteRune(c)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
