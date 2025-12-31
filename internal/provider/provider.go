// Package provider defines interfaces for LLM providers.
package provider

import (
	"context"
	"encoding/json"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/auth"
)

// Provider defines the interface for LLM providers.
type Provider interface {
	// ID returns the provider identifier (e.g., "chatgpt").
	ID() string

	// Models returns the list of models this provider supports.
	Models() []api.Model

	// SupportsModel checks if the provider supports a given model ID.
	// The modelID is without the provider prefix (e.g., "gpt-5.1-codex-high").
	// Providers can implement custom logic like effort suffix handling.
	SupportsModel(modelID string) bool

	// ChatCompletion sends a request and returns a stream.
	ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (Stream, error)
}

// ChatCompletionRequest is the provider-facing request.
type ChatCompletionRequest struct {
	Model            string
	Messages         []api.Message
	Tools            []api.Tool
	ToolChoice       json.RawMessage
	Stream           bool
	StreamOptions    *api.StreamOptions
	ReasoningEffort  string
	ReasoningSummary string // Override via X-Reasoning-Summary header
	ReasoningCompat  string // Override via X-Reasoning-Compat header
	TextVerbosity    string // Override via X-Text-Verbosity header

	// Optional parameters (supported by some providers like Copilot)
	Temperature         *float64
	TopP                *float64
	MaxTokens           *int
	MaxCompletionTokens *int
	Stop                json.RawMessage
	PresencePenalty     *float64
	FrequencyPenalty    *float64
	ResponseFormat      *api.ResponseFormat
	ParallelToolCalls   *bool
}

// Stream represents a streaming/non-streaming response.
type Stream interface {
	// Next returns the next chunk. Returns io.EOF when done.
	Next() (*api.ChatCompletionChunk, error)

	// Response returns the accumulated non-streaming response.
	// Call after Next() returns io.EOF.
	Response() *api.ChatCompletionResponse

	// Err returns any error that occurred during streaming.
	Err() error

	// Close releases resources.
	Close() error
}

// Authenticator is implemented by provider packages to handle login.
type Authenticator interface {
	// ProviderID returns the provider this authenticator is for.
	ProviderID() string

	// AuthMethod returns the auth method (OAuth, APIKey).
	AuthMethod() auth.AuthMethod

	// Login performs the provider-specific login flow.
	Login(ctx context.Context, store *auth.Store) error
}

// LifecycleProvider is an optional interface for providers that need
// startup/shutdown lifecycle management.
type LifecycleProvider interface {
	Provider

	// Init performs any initialization (e.g., prefetching).
	Init() error

	// Start begins background tasks (e.g., refresh goroutines).
	Start()

	// Close stops background tasks and releases resources.
	Close()
}

// Refresher is an optional interface for providers that support forced refresh.
type Refresher interface {
	// RefreshModels forces a refresh of the provider's models or data.
	RefreshModels(ctx context.Context) error
}
