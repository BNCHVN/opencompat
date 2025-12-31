package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/edgard/opencompat/internal/auth"
	"github.com/edgard/opencompat/internal/httputil"
)

// HTTP client configuration
const (
	HTTPTimeout = 5 * time.Minute // Long timeout for streaming responses

	// Codex CLI client identification - matches official client
	DefaultOriginator = "codex_cli_rs"
	CodexVersion      = "0.77.0" // Matches latest Codex CLI release
)

// userAgent is computed once at package initialization
var userAgent string

func init() {
	userAgent = httputil.BuildUserAgent(DefaultOriginator, CodexVersion)
}

// Client handles communication with the ChatGPT backend API.
type Client struct {
	httpClient     *http.Client
	store          *auth.Store
	cache          *InstructionsCache
	cfg            *Config
	cancelRefresh  context.CancelFunc
	refreshContext context.Context
}

// NewClient creates a new upstream client.
func NewClient(store *auth.Store, cfg *Config) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: HTTPTimeout,
		},
		store: store,
		cache: NewInstructionsCache(),
		cfg:   cfg,
	}
}

// PrefetchInstructions fetches all instruction files on startup.
// This should be called before starting the HTTP server.
// Returns error if instructions cannot be loaded (no cache AND GitHub down).
func (c *Client) PrefetchInstructions() error {
	return c.cache.Prefetch()
}

// StartBackgroundRefresh starts the background refresh goroutine.
// Call this after PrefetchInstructions succeeds.
func (c *Client) StartBackgroundRefresh() {
	if c.cancelRefresh != nil {
		// Already running
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.refreshContext = ctx
	c.cancelRefresh = cancel

	interval := time.Duration(c.cfg.InstructionsRefresh) * time.Minute
	c.cache.StartBackgroundRefresh(ctx, interval)
}

// Close stops the background refresh and cleans up resources.
func (c *Client) Close() {
	if c.cancelRefresh != nil {
		c.cancelRefresh()
		c.cancelRefresh = nil
	}
}

// SendRequest sends a chat completion request to ChatGPT and returns a reader for SSE events.
func (c *Client) SendRequest(ctx context.Context, req *ResponsesRequest) (*http.Response, error) {
	// Get auth credentials (auto-refreshes if expired)
	creds, err := c.store.GetOAuthCredentialsRefreshed("chatgpt", GetOAuthConfig())
	if err != nil {
		return nil, fmt.Errorf("auth error: %w", err)
	}

	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", ChatGPTResponsesURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Set headers to mimic Codex CLI client exactly
	httpReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
	httpReq.Header.Set("originator", DefaultOriginator)

	if creds.AccountID != "" {
		httpReq.Header.Set("ChatGPT-Account-ID", creds.AccountID)
	}

	if req.PromptCacheKey != "" {
		httpReq.Header.Set("session_id", req.PromptCacheKey)
		httpReq.Header.Set("conversation_id", req.PromptCacheKey)
	}

	// Send request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// GetInstructions fetches instructions for a model.
func (c *Client) GetInstructions(modelID string) (string, error) {
	return c.cache.Get(modelID)
}

// RefreshInstructions forces a refresh of all instruction files.
func (c *Client) RefreshInstructions(ctx context.Context) error {
	return c.cache.RefreshAll(ctx)
}
