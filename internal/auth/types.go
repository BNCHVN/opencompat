// Package auth provides OAuth authentication for the Codex API.
package auth

import "time"

// AuthMethod defines how a provider authenticates.
type AuthMethod int

const (
	// AuthMethodOAuth uses browser-based OAuth PKCE flow.
	AuthMethodOAuth AuthMethod = iota
	// AuthMethodAPIKey uses an API key prompt.
	AuthMethodAPIKey
	// AuthMethodDeviceFlow uses OAuth device authorization flow.
	AuthMethodDeviceFlow
)

// String returns the string representation of the auth method.
func (m AuthMethod) String() string {
	switch m {
	case AuthMethodOAuth:
		return "oauth"
	case AuthMethodAPIKey:
		return "api_key"
	case AuthMethodDeviceFlow:
		return "device_flow"
	default:
		return "unknown"
	}
}

// TokenData represents the OAuth token response.
type TokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

// OAuthCredentials contains the OAuth tokens and metadata stored on disk.
type OAuthCredentials struct {
	Type         string    `json:"type"` // Always "oauth"
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountID    string    `json:"account_id,omitempty"`
	Email        string    `json:"email,omitempty"`
}

// IsExpired returns true if the access token has expired.
func (c *OAuthCredentials) IsExpired() bool {
	// Consider expired 60 seconds before actual expiry for safety margin
	return time.Now().Add(60 * time.Second).After(c.ExpiresAt)
}

// IsValid returns true if the credentials have required tokens.
func (c *OAuthCredentials) IsValid() bool {
	return c.AccessToken != "" && c.RefreshToken != ""
}

// APIKeyCredentials contains an API key stored on disk.
// This is used by providers that authenticate via API key (e.g., Anthropic).
type APIKeyCredentials struct {
	Type      string    `json:"type"` // Always "api_key"
	APIKey    string    `json:"api_key"`
	CreatedAt time.Time `json:"created_at"`
}

// IsValid returns true if the credentials have an API key.
func (c *APIKeyCredentials) IsValid() bool {
	return c.APIKey != ""
}

// PKCEData holds the PKCE verifier and challenge for OAuth.
type PKCEData struct {
	Verifier  string
	Challenge string
}

// OAuthError represents an error response from the OAuth server.
type OAuthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// CredentialType is used to determine which credential struct to unmarshal into.
type CredentialType struct {
	Type string `json:"type"`
}

// OAuthConfig holds provider-specific OAuth configuration.
// Providers supply this to the auth package for OAuth operations.
type OAuthConfig struct {
	TokenURL     string // OAuth token endpoint
	AuthorizeURL string // OAuth authorization endpoint
	RedirectURI  string // OAuth callback URI
	CallbackPort int    // Port for callback server
	Scopes       string // OAuth scopes (space-separated)
	ClientID     string // OAuth client ID

	// ExtraAuthParams are additional parameters to include in the authorization URL.
	// This allows providers to specify non-standard OAuth parameters.
	ExtraAuthParams map[string]string

	// TokenExtractors are optional functions to extract additional info from tokens.
	// If nil, AccountID and Email will be empty in credentials.
	ExtractAccountID func(token string) (string, error)
	ExtractEmail     func(token string) (string, error)
}

// DeviceFlowConfig holds configuration for OAuth device authorization flow.
// This is used by providers like GitHub Copilot that use device code flow.
type DeviceFlowConfig struct {
	ClientID       string // OAuth client ID
	Scopes         string // OAuth scopes (space-separated)
	DeviceCodeURL  string // Device code request endpoint
	AccessTokenURL string // Token polling endpoint
	UserAgent      string // User-Agent header for API requests
}
