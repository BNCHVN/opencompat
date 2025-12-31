package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/edgard/opencompat/internal/config"
)

// Store manages credential persistence for all providers.
type Store struct {
	dataDir   string
	cache     map[string]any // providerID -> credentials
	cacheMu   sync.RWMutex
	refreshMu sync.Map // providerID -> *sync.Mutex (per-provider refresh locks)
}

// NewStore creates a new credential store.
func NewStore() *Store {
	return &Store{
		dataDir: config.DataDir(),
		cache:   make(map[string]any),
	}
}

// getRefreshMutex returns a per-provider mutex for refresh operations.
func (s *Store) getRefreshMutex(providerID string) *sync.Mutex {
	mu, _ := s.refreshMu.LoadOrStore(providerID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// credentialsPath returns the path for a provider's credentials file.
func (s *Store) credentialsPath(providerID string) string {
	return filepath.Join(s.dataDir, providerID+".json")
}

// copyOAuthCredentials returns a deep copy of OAuth credentials.
func copyOAuthCredentials(creds *OAuthCredentials) *OAuthCredentials {
	if creds == nil {
		return nil
	}
	return &OAuthCredentials{
		Type:         creds.Type,
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		IDToken:      creds.IDToken,
		ExpiresAt:    creds.ExpiresAt,
		AccountID:    creds.AccountID,
		Email:        creds.Email,
	}
}

// copyAPIKeyCredentials returns a deep copy of API key credentials.
func copyAPIKeyCredentials(creds *APIKeyCredentials) *APIKeyCredentials {
	if creds == nil {
		return nil
	}
	return &APIKeyCredentials{
		Type:      creds.Type,
		APIKey:    creds.APIKey,
		CreatedAt: creds.CreatedAt,
	}
}

// GetOAuthCredentials loads OAuth credentials for a provider.
// Returns a copy of the credentials to prevent cache corruption.
func (s *Store) GetOAuthCredentials(providerID string) (*OAuthCredentials, error) {
	s.cacheMu.RLock()
	if cached, ok := s.cache[providerID]; ok {
		if creds, ok := cached.(*OAuthCredentials); ok {
			s.cacheMu.RUnlock()
			return copyOAuthCredentials(creds), nil
		}
		// Wrong type in cache - need to evict, upgrade to write lock
		s.cacheMu.RUnlock()
		s.cacheMu.Lock()
		// Re-check after acquiring write lock (another goroutine may have fixed it)
		if cached, ok := s.cache[providerID]; ok {
			if creds, ok := cached.(*OAuthCredentials); ok {
				s.cacheMu.Unlock()
				return copyOAuthCredentials(creds), nil
			}
			// Still wrong type, evict it
			delete(s.cache, providerID)
		}
		s.cacheMu.Unlock()
	} else {
		s.cacheMu.RUnlock()
	}

	path := s.credentialsPath(providerID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not logged in to %s - run 'opencompat login %s' first", providerID, providerID)
		}
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	// Check credential type
	var typeCheck CredentialType
	if err := json.Unmarshal(data, &typeCheck); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}
	if typeCheck.Type != "oauth" {
		return nil, fmt.Errorf("expected oauth credentials for %s, got %s", providerID, typeCheck.Type)
	}

	var creds OAuthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	// Store in cache, but if another goroutine already populated it, return the cached value
	s.cacheMu.Lock()
	if cached, ok := s.cache[providerID]; ok {
		if cachedCreds, ok := cached.(*OAuthCredentials); ok {
			s.cacheMu.Unlock()
			return copyOAuthCredentials(cachedCreds), nil
		}
	}
	credsCopy := copyOAuthCredentials(&creds)
	s.cache[providerID] = credsCopy
	s.cacheMu.Unlock()

	// Return another copy to prevent caller from mutating the cached copy
	return copyOAuthCredentials(credsCopy), nil
}

// GetAPIKeyCredentials loads API key credentials for a provider.
// Returns a copy of the credentials to prevent cache corruption.
func (s *Store) GetAPIKeyCredentials(providerID string) (*APIKeyCredentials, error) {
	s.cacheMu.RLock()
	if cached, ok := s.cache[providerID]; ok {
		if creds, ok := cached.(*APIKeyCredentials); ok {
			s.cacheMu.RUnlock()
			return copyAPIKeyCredentials(creds), nil
		}
		// Wrong type in cache - need to evict, upgrade to write lock
		s.cacheMu.RUnlock()
		s.cacheMu.Lock()
		// Re-check after acquiring write lock (another goroutine may have fixed it)
		if cached, ok := s.cache[providerID]; ok {
			if creds, ok := cached.(*APIKeyCredentials); ok {
				s.cacheMu.Unlock()
				return copyAPIKeyCredentials(creds), nil
			}
			// Still wrong type, evict it
			delete(s.cache, providerID)
		}
		s.cacheMu.Unlock()
	} else {
		s.cacheMu.RUnlock()
	}

	path := s.credentialsPath(providerID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not logged in to %s - run 'opencompat login %s' first", providerID, providerID)
		}
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	// Check credential type
	var typeCheck CredentialType
	if err := json.Unmarshal(data, &typeCheck); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}
	if typeCheck.Type != "api_key" {
		return nil, fmt.Errorf("expected api_key credentials for %s, got %s", providerID, typeCheck.Type)
	}

	var creds APIKeyCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	// Store in cache, but if another goroutine already populated it, return the cached value
	s.cacheMu.Lock()
	if cached, ok := s.cache[providerID]; ok {
		if cachedCreds, ok := cached.(*APIKeyCredentials); ok {
			s.cacheMu.Unlock()
			return copyAPIKeyCredentials(cachedCreds), nil
		}
	}
	credsCopy := copyAPIKeyCredentials(&creds)
	s.cache[providerID] = credsCopy
	s.cacheMu.Unlock()

	// Return another copy to prevent caller from mutating the cached copy
	return copyAPIKeyCredentials(credsCopy), nil
}

// SaveOAuthCredentials stores OAuth credentials for a provider.
func (s *Store) SaveOAuthCredentials(providerID string, creds *OAuthCredentials) error {
	if err := config.EnsureDataDir(); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Copy to avoid mutating caller's object
	credsCopy := copyOAuthCredentials(creds)
	credsCopy.Type = "oauth"

	data, err := json.MarshalIndent(credsCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	path := s.credentialsPath(providerID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}

	// Store in cache (credsCopy is already a copy, safe to store directly)
	s.cacheMu.Lock()
	s.cache[providerID] = credsCopy
	s.cacheMu.Unlock()

	return nil
}

// SaveAPIKeyCredentials stores API key credentials for a provider.
func (s *Store) SaveAPIKeyCredentials(providerID string, creds *APIKeyCredentials) error {
	if err := config.EnsureDataDir(); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Copy to avoid mutating caller's object
	credsCopy := copyAPIKeyCredentials(creds)
	credsCopy.Type = "api_key"

	data, err := json.MarshalIndent(credsCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	path := s.credentialsPath(providerID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}

	// Store in cache (credsCopy is already a copy, safe to store directly)
	s.cacheMu.Lock()
	s.cache[providerID] = credsCopy
	s.cacheMu.Unlock()

	return nil
}

// DeleteCredentials removes credentials for a provider.
func (s *Store) DeleteCredentials(providerID string) error {
	s.cacheMu.Lock()
	delete(s.cache, providerID)
	s.cacheMu.Unlock()

	path := s.credentialsPath(providerID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete credentials: %w", err)
	}
	return nil
}

// IsLoggedIn checks if a provider has valid credentials.
func (s *Store) IsLoggedIn(providerID string) bool {
	path := s.credentialsPath(providerID)
	_, err := os.Stat(path)
	return err == nil
}

// SetOAuthFromTokenData creates OAuth credentials from token response and saves them.
// The oauthCfg is used to extract account ID and email from tokens using provider-specific extractors.
func (s *Store) SetOAuthFromTokenData(providerID string, tokens *TokenData, oauthCfg *OAuthConfig) error {
	expiresAt := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)

	creds := &OAuthCredentials{
		Type:         "oauth",
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		IDToken:      tokens.IDToken,
		ExpiresAt:    expiresAt,
	}

	// Extract account ID using provider's extractor if available
	if oauthCfg != nil && oauthCfg.ExtractAccountID != nil {
		if tokens.IDToken != "" {
			if accountID, err := oauthCfg.ExtractAccountID(tokens.IDToken); err == nil {
				creds.AccountID = accountID
			}
		}
		if creds.AccountID == "" {
			if accountID, err := oauthCfg.ExtractAccountID(tokens.AccessToken); err == nil {
				creds.AccountID = accountID
			}
		}
	}

	// Extract email using provider's extractor if available
	if oauthCfg != nil && oauthCfg.ExtractEmail != nil {
		if tokens.IDToken != "" {
			if email, err := oauthCfg.ExtractEmail(tokens.IDToken); err == nil {
				creds.Email = email
			}
		}
		if creds.Email == "" {
			if email, err := oauthCfg.ExtractEmail(tokens.AccessToken); err == nil {
				creds.Email = email
			}
		}
	}

	return s.SaveOAuthCredentials(providerID, creds)
}

// RefreshOAuth refreshes OAuth tokens for a provider using the given OAuth config.
func (s *Store) RefreshOAuth(providerID string, oauthCfg *OAuthConfig) error {
	creds, err := s.GetOAuthCredentials(providerID)
	if err != nil {
		return err
	}

	if creds.RefreshToken == "" {
		return errors.New("no refresh token available")
	}

	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {creds.RefreshToken},
		"client_id":     {oauthCfg.ClientID},
	}

	req, err := http.NewRequest("POST", oauthCfg.TokenURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		var oauthErr OAuthError
		if json.Unmarshal(body, &oauthErr) == nil && oauthErr.Error != "" {
			return fmt.Errorf("token refresh failed: %s - %s", oauthErr.Error, oauthErr.ErrorDescription)
		}
		return fmt.Errorf("token refresh failed with status %d", resp.StatusCode)
	}

	var tokens TokenData
	if err := json.Unmarshal(body, &tokens); err != nil {
		return err
	}

	// Keep the old refresh token if a new one wasn't provided
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = creds.RefreshToken
	}

	return s.SetOAuthFromTokenData(providerID, &tokens, oauthCfg)
}

// GetOAuthCredentialsRefreshed gets OAuth credentials, refreshing if expired.
// The oauthCfg is used for token refresh if needed.
// Uses per-provider mutex to prevent concurrent refresh attempts.
func (s *Store) GetOAuthCredentialsRefreshed(providerID string, oauthCfg *OAuthConfig) (*OAuthCredentials, error) {
	creds, err := s.GetOAuthCredentials(providerID)
	if err != nil {
		return nil, err
	}

	if creds.IsExpired() {
		// Acquire per-provider refresh lock to prevent concurrent refreshes
		refreshMu := s.getRefreshMutex(providerID)
		refreshMu.Lock()
		defer refreshMu.Unlock()

		// Re-check after acquiring lock (another goroutine may have refreshed)
		creds, err = s.GetOAuthCredentials(providerID)
		if err != nil {
			return nil, err
		}

		if creds.IsExpired() {
			if err := s.RefreshOAuth(providerID, oauthCfg); err != nil {
				return nil, fmt.Errorf("failed to refresh token: %w", err)
			}
			// Re-read the refreshed credentials
			creds, err = s.GetOAuthCredentials(providerID)
			if err != nil {
				return nil, err
			}
		}
	}

	return creds, nil
}
