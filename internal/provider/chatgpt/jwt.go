package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// JWTClaims represents the claims in an OpenAI JWT token.
type JWTClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	// The auth claim is nested under the OpenAI-specific claim path
	Auth *AuthClaim `json:"https://api.openai.com/auth,omitempty"`
}

// AuthClaim contains OpenAI-specific auth information.
type AuthClaim struct {
	UserID           string   `json:"user_id,omitempty"`
	ChatGPTAccountID string   `json:"chatgpt_account_id,omitempty"`
	OrganizationID   string   `json:"organization_id,omitempty"`
	ProjectID        string   `json:"project_id,omitempty"`
	APIKeyIDs        []string `json:"api_key_ids,omitempty"`
}

// DecodeJWT decodes a JWT token without verification.
// This is safe because we receive the token directly from OpenAI over HTTPS.
func DecodeJWT(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid JWT format")
	}

	// Decode the payload (second part)
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, err
	}

	var claims JWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}

	return &claims, nil
}

// ExtractAccountID extracts the ChatGPT account ID from a JWT token.
// The account ID is chatgpt_account_id from the auth claim.
func ExtractAccountID(token string) (string, error) {
	claims, err := DecodeJWT(token)
	if err != nil {
		return "", err
	}

	// Try to get chatgpt_account_id from auth claim first
	if claims.Auth != nil && claims.Auth.ChatGPTAccountID != "" {
		return claims.Auth.ChatGPTAccountID, nil
	}

	// Fall back to user_id
	if claims.Auth != nil && claims.Auth.UserID != "" {
		return claims.Auth.UserID, nil
	}

	// Fall back to sub claim
	if claims.Sub != "" {
		return claims.Sub, nil
	}

	return "", errors.New("no account ID found in token")
}

// ExtractEmail extracts the email from a JWT token.
func ExtractEmail(token string) (string, error) {
	claims, err := DecodeJWT(token)
	if err != nil {
		return "", err
	}
	return claims.Email, nil
}

// base64URLDecode decodes a base64url string with or without padding.
func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}
