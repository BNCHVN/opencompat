package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/httputil"
)

const (
	// CopilotModelsURL is the endpoint for fetching available models.
	CopilotModelsURL = CopilotBaseURL + "/models"

	// ModelsDiskCacheTTL is how long disk cache is valid (7 days).
	ModelsDiskCacheTTL = 7 * 24 * time.Hour
)

// ModelsCache manages caching of Copilot models.
type ModelsCache struct {
	mu             sync.RWMutex
	models         []api.Model
	modelIDs       map[string]bool
	fetchedAt      time.Time
	client         *Client
	cacheTTL       time.Duration
	stopRefresh    chan struct{}
	refreshDone    chan struct{}
	refreshStarted bool
}

// NewModelsCache creates a new models cache.
func NewModelsCache(client *Client, refreshMinutes int) *ModelsCache {
	return &ModelsCache{
		client:      client,
		modelIDs:    make(map[string]bool),
		cacheTTL:    time.Duration(refreshMinutes) * time.Minute,
		stopRefresh: make(chan struct{}),
		refreshDone: make(chan struct{}),
	}
}

// GetModels returns the list of available models.
// Returns empty list if not logged in and no cache exists.
func (c *ModelsCache) GetModels() []api.Model {
	c.mu.RLock()
	if len(c.models) > 0 && time.Since(c.fetchedAt) < c.cacheTTL {
		models := c.models
		c.mu.RUnlock()
		return models
	}
	c.mu.RUnlock()

	// Try to refresh
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if len(c.models) > 0 && time.Since(c.fetchedAt) < c.cacheTTL {
		return c.models
	}

	// If no client or not logged in, try disk cache only
	if c.client == nil || c.client.store == nil {
		models, err := c.loadFromDisk()
		if err == nil && len(models) > 0 {
			c.updateCache(models)
			return c.models
		}
		// Return empty list - user needs to login
		return nil
	}

	// Try to fetch from API
	models, err := c.fetchFromAPI()
	if err == nil {
		c.updateCache(models)
		// Save to disk asynchronously
		go c.saveToDisk()
		return c.models
	}

	slog.Warn("failed to fetch models from API", "provider", "copilot", "error", err)

	// Try disk cache as fallback
	models, err = c.loadFromDisk()
	if err == nil && len(models) > 0 {
		slog.Debug("using cached models from disk", "provider", "copilot")
		c.updateCache(models)
		return c.models
	}

	// Return empty list - couldn't fetch and no cache
	return nil
}

// SupportsModel checks if a model ID is supported.
func (c *ModelsCache) SupportsModel(modelID string) bool {
	c.mu.RLock()
	if len(c.modelIDs) == 0 {
		c.mu.RUnlock()
		c.GetModels() // Populate cache
		c.mu.RLock()
	}
	supported := c.modelIDs[modelID]
	c.mu.RUnlock()
	return supported
}

// RefreshModels forces a refresh of the models list.
func (c *ModelsCache) RefreshModels(ctx context.Context) error {
	models, err := c.fetchFromAPIWithContext(ctx)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.updateCache(models)
	c.mu.Unlock()

	go c.saveToDisk()
	return nil
}

// updateCache updates the in-memory cache (must hold write lock).
func (c *ModelsCache) updateCache(models []api.Model) {
	c.models = models
	c.modelIDs = make(map[string]bool, len(models))
	for _, m := range models {
		c.modelIDs[m.ID] = true
	}
	c.fetchedAt = time.Now()
}

// fetchFromAPI fetches models from the Copilot API.
func (c *ModelsCache) fetchFromAPI() ([]api.Model, error) {
	return c.fetchFromAPIWithContext(context.Background())
}

// fetchFromAPIWithContext fetches models from the Copilot API with context.
func (c *ModelsCache) fetchFromAPIWithContext(ctx context.Context) ([]api.Model, error) {
	if c.client == nil {
		return nil, fmt.Errorf("no client configured")
	}

	// Get valid Copilot token
	token, err := c.client.getCopilotToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", CopilotModelsURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httputil.BuildUserAgent("GitHubCopilotChat", "0.22.4"))
	req.Header.Set("Editor-Version", EditorVersion)
	req.Header.Set("Editor-Plugin-Version", EditorPluginVersion)
	req.Header.Set("Copilot-Integration-Id", CopilotIntegrationID)

	resp, err := c.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Version     string `json:"version"`
			ModelFamily string `json:"model_family"`
			Vendor      string `json:"vendor"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse models response: %w", err)
	}

	models := make([]api.Model, 0, len(response.Data))
	for _, m := range response.Data {
		ownedBy := m.Vendor
		if ownedBy == "" {
			ownedBy = "unknown"
		}
		models = append(models, api.Model{
			ID:      m.ID,
			Object:  "model",
			OwnedBy: ownedBy,
		})
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no models returned from API")
	}

	return models, nil
}

// Disk cache helpers

type modelsCacheMeta struct {
	FetchedAt time.Time   `json:"fetched_at"`
	Models    []api.Model `json:"models"`
}

func (c *ModelsCache) cacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "opencompat", "copilot")
}

func (c *ModelsCache) saveToDisk() {
	cacheDir := c.cacheDir()
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		slog.Warn("failed to create cache directory", "error", err)
		return
	}

	c.mu.RLock()
	meta := modelsCacheMeta{
		FetchedAt: c.fetchedAt,
		Models:    c.models,
	}
	c.mu.RUnlock()

	data, err := json.Marshal(meta)
	if err != nil {
		slog.Warn("failed to marshal models cache", "error", err)
		return
	}

	cachePath := filepath.Join(cacheDir, "models.json")
	if err := os.WriteFile(cachePath, data, 0600); err != nil {
		slog.Warn("failed to write models cache", "error", err)
	}
}

func (c *ModelsCache) loadFromDisk() ([]api.Model, error) {
	cachePath := filepath.Join(c.cacheDir(), "models.json")

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}

	var meta modelsCacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	// Check if disk cache is too old
	if time.Since(meta.FetchedAt) > ModelsDiskCacheTTL {
		slog.Warn("disk cache expired",
			"provider", "copilot",
			"age", time.Since(meta.FetchedAt),
		)
	}

	return meta.Models, nil
}

// StartBackgroundRefresh starts a goroutine that periodically refreshes the models.
func (c *ModelsCache) StartBackgroundRefresh() {
	if c.cacheTTL <= 0 {
		return
	}

	c.mu.Lock()
	if c.refreshStarted {
		c.mu.Unlock()
		return
	}
	c.refreshStarted = true
	c.mu.Unlock()

	slog.Debug("background models refresh started", "provider", "copilot", "interval", c.cacheTTL)

	go func() {
		defer close(c.refreshDone)

		ticker := time.NewTicker(c.cacheTTL)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopRefresh:
				slog.Debug("background models refresh stopped", "provider", "copilot")
				return
			case <-ticker.C:
				slog.Debug("background models refresh triggered", "provider", "copilot")
				if err := c.RefreshModels(context.Background()); err != nil {
					slog.Warn("failed to refresh models", "provider", "copilot", "error", err)
				}
			}
		}
	}()
}

// StopBackgroundRefresh stops the background refresh goroutine.
func (c *ModelsCache) StopBackgroundRefresh() {
	c.mu.Lock()
	if !c.refreshStarted {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	select {
	case <-c.stopRefresh:
		// Already closed
	default:
		close(c.stopRefresh)
	}
	<-c.refreshDone
}
