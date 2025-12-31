package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/auth"
	"github.com/edgard/opencompat/internal/httputil"
)

// CopilotToken represents a token obtained from the Copilot API.
type CopilotToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Client handles communication with the Copilot API.
type Client struct {
	store        *auth.Store
	httpClient   *http.Client
	mu           sync.RWMutex
	copilotToken *CopilotToken
}

// NewClient creates a new Copilot client.
func NewClient(store *auth.Store) *Client {
	return &Client{
		store: store,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// getGitHubToken retrieves the GitHub OAuth token (stored as refresh token).
func (c *Client) getGitHubToken() (string, error) {
	creds, err := c.store.GetOAuthCredentials(ProviderID)
	if err != nil {
		return "", fmt.Errorf("failed to get credentials: %w", err)
	}
	if creds.RefreshToken == "" {
		return "", fmt.Errorf("no GitHub token found - please run: opencompat login %s", ProviderID)
	}
	return creds.RefreshToken, nil
}

// getCopilotToken returns a valid Copilot API token, refreshing if necessary.
func (c *Client) getCopilotToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.copilotToken != nil && time.Now().Add(60*time.Second).Before(c.copilotToken.ExpiresAt) {
		token := c.copilotToken.Token
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	// Need to refresh token
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.copilotToken != nil && time.Now().Add(60*time.Second).Before(c.copilotToken.ExpiresAt) {
		return c.copilotToken.Token, nil
	}

	// Get GitHub token
	githubToken, err := c.getGitHubToken()
	if err != nil {
		return "", err
	}

	// Exchange for Copilot token
	token, err := c.refreshCopilotToken(ctx, githubToken)
	if err != nil {
		return "", err
	}

	c.copilotToken = token
	return token.Token, nil
}

// refreshCopilotToken exchanges a GitHub token for a Copilot API token.
func (c *Client) refreshCopilotToken(ctx context.Context, githubToken string) (*CopilotToken, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", CopilotTokenURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgentValue)
	req.Header.Set("Editor-Version", EditorVersion)
	req.Header.Set("Editor-Plugin-Version", EditorPluginVersion)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request Copilot token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &CopilotToken{
		Token:     tokenResp.Token,
		ExpiresAt: time.Unix(tokenResp.ExpiresAt, 0),
	}, nil
}

// SendRequest sends a chat completion request to the Copilot API.
func (c *Client) SendRequest(ctx context.Context, chatReq *api.ChatCompletionRequest) (*http.Response, error) {
	// Get valid Copilot token
	token, err := c.getCopilotToken(ctx)
	if err != nil {
		return nil, err
	}

	// Serialize request
	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", CopilotChatURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set required headers
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", httputil.BuildUserAgent("GitHubCopilotChat", "0.22.4"))
	req.Header.Set("Editor-Version", EditorVersion)
	req.Header.Set("Editor-Plugin-Version", EditorPluginVersion)
	req.Header.Set("Copilot-Integration-Id", CopilotIntegrationID)
	req.Header.Set("Openai-Intent", "conversation-panel")

	// Set X-Initiator header based on message content
	// "agent" if there are tool/assistant messages, else "user"
	initiator := "user"
	for _, msg := range chatReq.Messages {
		if msg.Role == "assistant" || msg.Role == "tool" {
			initiator = "agent"
			break
		}
	}
	req.Header.Set("X-Initiator", initiator)

	// Set Copilot-Vision-Request header if any message contains images
	hasVision := false
	for _, msg := range chatReq.Messages {
		if hasImageContent(msg) {
			hasVision = true
			break
		}
	}
	if hasVision {
		req.Header.Set("Copilot-Vision-Request", "true")
	}

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// hasImageContent checks if a message contains image content.
func hasImageContent(msg api.Message) bool {
	// Check if content is an array (multimodal)
	if msg.Content == nil {
		return false
	}

	// Try to parse as array of content parts
	contentBytes, err := json.Marshal(msg.Content)
	if err != nil {
		return false
	}

	var parts []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(contentBytes, &parts); err != nil {
		return false
	}

	for _, part := range parts {
		if part.Type == "image_url" || part.Type == "image" {
			return true
		}
	}

	return false
}
