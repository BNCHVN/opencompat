// Package config provides global configuration and XDG paths.
package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Application name for XDG paths
const AppName = "opencompat"

// Default server configuration
const (
	DefaultHost      = "127.0.0.1"
	DefaultPort      = 8080
	DefaultLogLevel  = "info"
	DefaultLogFormat = "text"
)

// Config holds global runtime configuration (server-level only).
// Provider-specific configuration is managed by each provider.
type Config struct {
	Host      string
	Port      int
	LogLevel  string // debug, info, warn, error
	LogFormat string // text, json
}

// Load reads global configuration from environment variables.
func Load() *Config {
	return &Config{
		Host:      getEnv("OPENCOMPAT_HOST", DefaultHost),
		Port:      getEnvInt("OPENCOMPAT_PORT", DefaultPort),
		LogLevel:  getEnv("OPENCOMPAT_LOG_LEVEL", DefaultLogLevel),
		LogFormat: getEnv("OPENCOMPAT_LOG_FORMAT", DefaultLogFormat),
	}
}

// DataDir returns the XDG data directory for the application.
// Uses $XDG_DATA_HOME/opencompat or ~/.local/share/opencompat
func DataDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, AppName)
}

// EnsureDataDir creates the data directory if it doesn't exist.
func EnsureDataDir() error {
	return os.MkdirAll(DataDir(), 0700)
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
