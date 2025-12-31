// Package auth handles OAuth PKCE authentication.
//
// The OAuth flow implemented here mirrors the authentication mechanism
// used by OpenAI's open-source Codex CLI (https://github.com/openai/codex,
// Apache 2.0 License). This implementation is original code that follows
// the same publicly documented OAuth PKCE standard.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

// PerformOAuthLogin performs the OAuth PKCE login flow for a provider.
// It opens a browser for authentication and waits for the callback.
func PerformOAuthLogin(store *Store, providerID string, oauthCfg *OAuthConfig) error {
	// Generate PKCE challenge
	pkce, err := GeneratePKCE()
	if err != nil {
		return fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state parameter
	state, err := GenerateState()
	if err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}

	// Build authorization URL
	authURL := buildAuthURL(pkce.Challenge, state, oauthCfg)

	// Create channel to receive the authorization code
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Start callback server
	server, err := startCallbackServer(state, codeChan, errChan, oauthCfg.CallbackPort)
	if err != nil {
		return fmt.Errorf("failed to start callback server: %w", err)
	}

	// Open browser
	fmt.Println("Opening browser for authentication...")
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Please open this URL in your browser:\n%s\n", authURL)
	}

	// Wait for callback with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeChan:
		// Success
	case err := <-errChan:
		_ = server.Shutdown(context.Background())
		return err
	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		return errors.New("login timed out")
	}

	// Shutdown the callback server
	_ = server.Shutdown(context.Background())

	// Exchange code for tokens
	tokens, err := exchangeCode(code, pkce.Verifier, oauthCfg)
	if err != nil {
		return fmt.Errorf("failed to exchange code: %w", err)
	}

	// Save tokens for the provider
	if err := store.SetOAuthFromTokenData(providerID, tokens, oauthCfg); err != nil {
		return fmt.Errorf("failed to save tokens: %w", err)
	}

	fmt.Println("Login successful!")
	return nil
}

func buildAuthURL(challenge, state string, oauthCfg *OAuthConfig) string {
	params := url.Values{
		"client_id":             {oauthCfg.ClientID},
		"redirect_uri":          {oauthCfg.RedirectURI},
		"response_type":         {"code"},
		"scope":                 {oauthCfg.Scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	// Add any extra provider-specific parameters
	for key, value := range oauthCfg.ExtraAuthParams {
		params.Set(key, value)
	}

	return oauthCfg.AuthorizeURL + "?" + params.Encode()
}

func startCallbackServer(expectedState string, codeChan chan string, errChan chan error, port int) (*http.Server, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		// Check for error response
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			errChan <- fmt.Errorf("OAuth error: %s - %s", errMsg, desc)
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, "<html><body><h1>Login Failed</h1><p>%s</p></body></html>", desc)
			return
		}

		// Verify state
		state := r.URL.Query().Get("state")
		if state != expectedState {
			errChan <- errors.New("state mismatch")
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html><body><h1>Login Failed</h1><p>State mismatch</p></body></html>")
			return
		}

		// Get authorization code
		code := r.URL.Query().Get("code")
		if code == "" {
			errChan <- errors.New("no authorization code received")
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html><body><h1>Login Failed</h1><p>No code received</p></body></html>")
			return
		}

		// Success response
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><h1>Login Successful!</h1><p>You can close this window.</p><script>window.close()</script></body></html>`)

		codeChan <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()

	return server, nil
}

func exchangeCode(code, verifier string, oauthCfg *OAuthConfig) (*TokenData, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthCfg.RedirectURI},
		"client_id":     {oauthCfg.ClientID},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequest("POST", oauthCfg.TokenURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
		var oauthErr OAuthError
		if json.Unmarshal(body, &oauthErr) == nil && oauthErr.Error != "" {
			return nil, fmt.Errorf("%s: %s", oauthErr.Error, oauthErr.ErrorDescription)
		}
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var tokens TokenData
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}

	return &tokens, nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
