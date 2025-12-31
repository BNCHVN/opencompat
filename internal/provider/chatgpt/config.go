package chatgpt

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/edgard/opencompat/internal/auth"
)

// Environment variable names for ChatGPT provider
const (
	EnvInstructionsRefresh = "OPENCOMPAT_CHATGPT_INSTRUCTIONS_REFRESH"
)

// Default values
const (
	DefaultReasoningEffort     = "medium"
	DefaultReasoningSummary    = "auto"
	DefaultReasoningCompat     = "none"
	DefaultTextVerbosity       = "medium"
	DefaultInstructionsRefresh = 24 * 60 // 24 hours in minutes
	OAuthClientID              = "app_EMoamEEZ73f0CkXaXp7hrann"
)

// API endpoints and constants
const (
	ChatGPTResponsesURL = "https://chatgpt.com/backend-api/codex/responses"
	GitHubReleasesAPI   = "https://api.github.com/repos/openai/codex/releases/latest"
	GitHubRawBaseURL    = "https://raw.githubusercontent.com/openai/codex"

	// Cache TTL in minutes
	InstructionsDiskCacheTTL = 7 * 24 * 60 // 7 days for disk cache
)

// OAuth constants for OpenAI authentication
const (
	OAuthTokenURL     = "https://auth.openai.com/oauth/token"
	OAuthAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	OAuthCallbackPort = 1455
	OAuthRedirectURI  = "http://localhost:1455/auth/callback"
	OAuthScopes       = "openid profile email offline_access"
)

// OAuthExtraParams contains OpenAI-specific OAuth parameters
var OAuthExtraParams = map[string]string{
	"id_token_add_organizations": "true",
	"codex_cli_simplified_flow":  "true",
	"originator":                 "codex_cli_rs",
}

// AppName for XDG paths (shared with global config)
const AppName = "opencompat"

// Config holds ChatGPT-specific configuration.
type Config struct {
	ReasoningEffort     string // none, low, medium, high, xhigh (default, overridable via header)
	ReasoningSummary    string // auto, concise, detailed (default, overridable via header)
	ReasoningCompat     string // none, think-tags, o3, legacy (default, overridable via header)
	TextVerbosity       string // low, medium, high (default, overridable via header)
	InstructionsRefresh int    // refresh interval in minutes
}

// LoadConfig reads ChatGPT configuration from environment variables.
func LoadConfig() *Config {
	return &Config{
		ReasoningEffort:     DefaultReasoningEffort,
		ReasoningSummary:    DefaultReasoningSummary,
		ReasoningCompat:     DefaultReasoningCompat,
		TextVerbosity:       DefaultTextVerbosity,
		InstructionsRefresh: getEnvInt(EnvInstructionsRefresh, DefaultInstructionsRefresh),
	}
}

// EnvVarDocs returns documentation for environment variables.
// Used by main.go to display help text.
func EnvVarDocs() []EnvVarDoc {
	return []EnvVarDoc{
		{Name: EnvInstructionsRefresh, Description: "Instructions refresh interval in minutes", Default: strconv.Itoa(DefaultInstructionsRefresh)},
	}
}

// EnvVarDoc documents an environment variable.
type EnvVarDoc struct {
	Name        string
	Description string
	Default     string
}

// CacheDir returns the XDG cache directory for the application.
func CacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, AppName)
}

// EnsureCacheDir creates the cache directory if it doesn't exist.
func EnsureCacheDir() error {
	return os.MkdirAll(CacheDir(), 0700)
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

// GetOAuthConfig returns the OAuth configuration for ChatGPT.
// Returns a fresh copy each time to prevent mutation of shared state.
func GetOAuthConfig() *auth.OAuthConfig {
	// Copy the extra params map to prevent mutation of the shared map
	extraParams := make(map[string]string, len(OAuthExtraParams))
	for k, v := range OAuthExtraParams {
		extraParams[k] = v
	}

	return &auth.OAuthConfig{
		TokenURL:         OAuthTokenURL,
		AuthorizeURL:     OAuthAuthorizeURL,
		RedirectURI:      OAuthRedirectURI,
		CallbackPort:     OAuthCallbackPort,
		Scopes:           OAuthScopes,
		ClientID:         OAuthClientID,
		ExtraAuthParams:  extraParams,
		ExtractAccountID: ExtractAccountID,
		ExtractEmail:     ExtractEmail,
	}
}
