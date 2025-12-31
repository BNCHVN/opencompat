package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DeviceCodeResponse represents the response from the device code endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceFlowTokenResponse represents the token response during polling.
type DeviceFlowTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// PerformDeviceFlowLogin performs the OAuth device authorization flow.
// This flow is used by providers like GitHub that support device code authentication.
func PerformDeviceFlowLogin(store *Store, providerID string, cfg *DeviceFlowConfig) error {
	// Step 1: Request device code
	deviceCode, err := requestDeviceCode(cfg)
	if err != nil {
		return fmt.Errorf("failed to request device code: %w", err)
	}

	// Step 2: Display instructions to user
	fmt.Println()
	fmt.Println("To authenticate, please:")
	fmt.Printf("  1. Open: %s\n", deviceCode.VerificationURI)
	fmt.Printf("  2. Enter code: %s\n", deviceCode.UserCode)
	fmt.Println()

	// Try to open browser
	if err := openBrowser(deviceCode.VerificationURI); err != nil {
		fmt.Println("Could not open browser automatically. Please open the URL manually.")
	}

	fmt.Println("Waiting for authorization...")

	// Step 3: Poll for token
	interval := deviceCode.Interval
	if interval < 5 {
		interval = 5 // Minimum 5 seconds as per RFC 8628
	}

	deadline := time.Now().Add(time.Duration(deviceCode.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)

		token, err := pollForToken(cfg, deviceCode.DeviceCode)
		if err != nil {
			// Check for specific errors
			var pollErr *deviceFlowPollError
			if errors.As(err, &pollErr) {
				switch pollErr.ErrorCode {
				case "authorization_pending":
					// Continue polling
					continue
				case "slow_down":
					// Increase interval
					interval += 5
					continue
				case "expired_token":
					return errors.New("authorization request expired - please try again")
				case "access_denied":
					return errors.New("authorization was denied by the user")
				default:
					return fmt.Errorf("authorization failed: %s", pollErr.ErrorCode)
				}
			}
			return fmt.Errorf("failed to poll for token: %w", err)
		}

		// Success! Save the token
		// For device flow, the access_token from GitHub becomes our refresh token
		// (used to get Copilot API tokens). We'll get the actual access token
		// when making API requests.
		creds := &OAuthCredentials{
			Type:         "oauth",
			RefreshToken: token.AccessToken, // GitHub token stored as refresh token
			AccessToken:  "",                // Will be populated by provider on first use
			ExpiresAt:    time.Time{},       // Will be populated by provider on first use
		}

		if err := store.SaveOAuthCredentials(providerID, creds); err != nil {
			return fmt.Errorf("failed to save credentials: %w", err)
		}

		fmt.Println("Login successful!")
		return nil
	}

	return errors.New("authorization request timed out - please try again")
}

// requestDeviceCode requests a device code from the authorization server.
func requestDeviceCode(cfg *DeviceFlowConfig) (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {cfg.ClientID},
		"scope":     {cfg.Scopes},
	}

	req, err := http.NewRequest("POST", cfg.DeviceCodeURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.UserAgent != "" {
		req.Header.Set("User-Agent", cfg.UserAgent)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var deviceCode DeviceCodeResponse
	if err := json.Unmarshal(body, &deviceCode); err != nil {
		return nil, err
	}

	return &deviceCode, nil
}

// deviceFlowPollError represents an error during token polling.
type deviceFlowPollError struct {
	ErrorCode string
}

func (e *deviceFlowPollError) Error() string {
	return fmt.Sprintf("device flow poll error: %s", e.ErrorCode)
}

// pollForToken polls the token endpoint for an access token.
func pollForToken(cfg *DeviceFlowConfig, deviceCode string) (*DeviceFlowTokenResponse, error) {
	data := url.Values{
		"client_id":   {cfg.ClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	req, err := http.NewRequest("POST", cfg.AccessTokenURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.UserAgent != "" {
		req.Header.Set("User-Agent", cfg.UserAgent)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tokenResp DeviceFlowTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	// Check for error in response
	if tokenResp.Error != "" {
		return nil, &deviceFlowPollError{ErrorCode: tokenResp.Error}
	}

	// Check for success
	if tokenResp.AccessToken != "" {
		return &tokenResp, nil
	}

	// No error and no token - shouldn't happen
	return nil, errors.New("unexpected response from token endpoint")
}
