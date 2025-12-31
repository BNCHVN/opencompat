package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GeneratePKCE creates a new PKCE code verifier and challenge.
// The verifier is a random 32-byte string, base64url-encoded.
// The challenge is the SHA256 hash of the verifier, base64url-encoded.
func GeneratePKCE() (*PKCEData, error) {
	// Generate 32 random bytes for the verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, err
	}

	// Base64url encode the verifier (no padding)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// SHA256 hash the verifier and base64url encode it
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEData{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

// GenerateState creates a random state parameter for OAuth.
func GenerateState() (string, error) {
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(stateBytes), nil
}
