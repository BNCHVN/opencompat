package provider

import (
	"fmt"
	"sort"
	"strings"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/auth"
)

// ProviderFactory creates a provider instance.
type ProviderFactory func(store *auth.Store) (Provider, error)

// EnvVarDoc documents an environment variable.
type EnvVarDoc struct {
	Name        string
	Description string
	Default     string
}

// ProviderMeta contains metadata about a provider type.
type ProviderMeta struct {
	ID            string
	Name          string // Human-readable name (e.g., "ChatGPT")
	AuthMethod    auth.AuthMethod
	OAuthCfg      *auth.OAuthConfig      // OAuth configuration (for OAuth providers)
	DeviceFlowCfg *auth.DeviceFlowConfig // Device flow config (for device flow providers)
	EnvVars       []EnvVarDoc            // Environment variable documentation
	Factory       ProviderFactory
}

// Registry manages providers.
type Registry struct {
	metas     map[string]ProviderMeta // All known providers
	providers map[string]Provider     // Active providers (logged in)
}

// NewRegistry creates a new registry.
func NewRegistry() *Registry {
	return &Registry{
		metas:     make(map[string]ProviderMeta),
		providers: make(map[string]Provider),
	}
}

// providerRegistrations holds all provider registrations.
// Providers call AddRegistration in their init() functions.
var providerRegistrations []func(*Registry)

// AddRegistration adds a provider registration function.
// Called by provider packages in their init() functions.
func AddRegistration(fn func(*Registry)) {
	providerRegistrations = append(providerRegistrations, fn)
}

// RegisterAll registers all known providers.
// This calls registration functions added via AddRegistration.
func RegisterAll(r *Registry) {
	for _, fn := range providerRegistrations {
		fn(r)
	}
}

// RegisterMeta registers a provider type.
func (r *Registry) RegisterMeta(meta ProviderMeta) {
	r.metas[meta.ID] = meta
}

// Initialize creates provider instances for all logged-in providers.
func (r *Registry) Initialize(store *auth.Store) error {
	for id, meta := range r.metas {
		if !store.IsLoggedIn(id) {
			continue // Silent skip - provider not logged in
		}

		p, err := meta.Factory(store)
		if err != nil {
			return fmt.Errorf("failed to initialize provider %s: %w", id, err)
		}
		if p != nil {
			r.providers[id] = p
		}
	}
	return nil
}

// GetMeta returns metadata for a provider (for login command).
func (r *Registry) GetMeta(providerID string) (ProviderMeta, bool) {
	meta, ok := r.metas[providerID]
	return meta, ok
}

// ListMetas returns all known provider metadata, sorted by ID.
func (r *Registry) ListMetas() []ProviderMeta {
	var metas []ProviderMeta
	for _, m := range r.metas {
		metas = append(metas, m)
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].ID < metas[j].ID
	})
	return metas
}

// ParseModel splits "chatgpt/gpt-5-codex" into ("chatgpt", "gpt-5-codex").
// Returns error if no prefix provided.
func ParseModel(model string) (providerID, modelID string, err error) {
	idx := strings.Index(model, "/")
	if idx == -1 {
		return "", "", fmt.Errorf("model must include provider prefix (e.g., 'chatgpt/gpt-5-codex'), got: %s", model)
	}
	return model[:idx], model[idx+1:], nil
}

// GetProvider returns the provider for a model string.
func (r *Registry) GetProvider(model string) (Provider, string, error) {
	providerID, modelID, err := ParseModel(model)
	if err != nil {
		return nil, "", err
	}

	p, ok := r.providers[providerID]
	if !ok {
		// Check if provider is known but not logged in
		if _, known := r.metas[providerID]; known {
			return nil, "", fmt.Errorf("provider '%s' requires login (run: opencompat login %s)", providerID, providerID)
		}
		return nil, "", fmt.Errorf("unknown provider: %s", providerID)
	}

	return p, modelID, nil
}

// AllModels returns all models from all active providers, prefixed with provider ID.
func (r *Registry) AllModels() []api.Model {
	var models []api.Model
	for _, p := range r.providers {
		for _, m := range p.Models() {
			// Prefix model ID with provider
			prefixed := m
			prefixed.ID = p.ID() + "/" + m.ID
			models = append(models, prefixed)
		}
	}
	// Sort for consistent ordering
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models
}

// IsModelSupported checks if a model (with prefix) is supported.
func (r *Registry) IsModelSupported(model string) bool {
	providerID, modelID, err := ParseModel(model)
	if err != nil {
		return false
	}

	p, ok := r.providers[providerID]
	if !ok {
		return false
	}

	return p.SupportsModel(modelID)
}

// HasProviders returns true if at least one provider is active.
func (r *Registry) HasProviders() bool {
	return len(r.providers) > 0
}

// GetActiveProvider returns an active provider by ID.
func (r *Registry) GetActiveProvider(providerID string) (Provider, bool) {
	p, ok := r.providers[providerID]
	return p, ok
}

// CloseAll closes all active providers that implement LifecycleProvider.
func (r *Registry) CloseAll() {
	for _, p := range r.providers {
		if lp, ok := p.(LifecycleProvider); ok {
			lp.Close()
		}
	}
}
