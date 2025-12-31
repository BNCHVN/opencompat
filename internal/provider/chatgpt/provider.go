// Package chatgpt implements the ChatGPT provider.
package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/auth"
	"github.com/edgard/opencompat/internal/provider"
	"github.com/edgard/opencompat/internal/sse"
)

const ProviderID = "chatgpt"

func init() {
	provider.AddRegistration(func(r *provider.Registry) {
		r.RegisterMeta(provider.ProviderMeta{
			ID:         ProviderID,
			Name:       "ChatGPT",
			AuthMethod: auth.AuthMethodOAuth,
			OAuthCfg:   GetOAuthConfig(),
			EnvVars:    convertEnvVarDocs(EnvVarDocs()),
			Factory:    New,
		})
	})
}

// convertEnvVarDocs converts chatgpt.EnvVarDoc to provider.EnvVarDoc.
func convertEnvVarDocs(docs []EnvVarDoc) []provider.EnvVarDoc {
	result := make([]provider.EnvVarDoc, len(docs))
	for i, d := range docs {
		result[i] = provider.EnvVarDoc{
			Name:        d.Name,
			Description: d.Description,
			Default:     d.Default,
		}
	}
	return result
}

// Provider implements the ChatGPT provider.
type Provider struct {
	client *Client
	cfg    *Config
}

// New creates a new ChatGPT provider.
func New(store *auth.Store) (provider.Provider, error) {
	cfg := LoadConfig()
	return &Provider{
		client: NewClient(store, cfg),
		cfg:    cfg,
	}, nil
}

// ID returns the provider identifier.
func (p *Provider) ID() string {
	return ProviderID
}

// Models returns the list of supported models.
func (p *Provider) Models() []api.Model {
	// Return models without provider prefix (registry will add it)
	return []api.Model{
		{ID: "gpt-5.2-codex", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5.1-codex-max", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5.1-codex", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5-codex", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5.1-codex-mini", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5.2", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5.1", Object: "model", OwnedBy: "openai"},
		{ID: "gpt-5", Object: "model", OwnedBy: "openai"},
	}
}

// SupportsModel checks if a model ID is supported, including effort suffixes.
func (p *Provider) SupportsModel(modelID string) bool {
	// Normalize model name (handles aliases and effort suffixes)
	normalizedModel, _ := NormalizeModelNameWithEffort(modelID)

	// Check if normalized model is in our list
	for _, m := range p.Models() {
		if m.ID == normalizedModel {
			return true
		}
	}
	return false
}

// ChatCompletion sends a chat completion request.
func (p *Provider) ChatCompletion(ctx context.Context, req *provider.ChatCompletionRequest) (provider.Stream, error) {
	// Get instructions for the model
	normalizedModel, _ := NormalizeModelNameWithEffort(req.Model)
	instructions, err := p.client.GetInstructions(normalizedModel)
	if err != nil {
		return nil, err
	}

	// Convert provider request to API request
	apiReq := &api.ChatCompletionRequest{
		Model:           req.Model,
		Messages:        req.Messages,
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		Stream:          req.Stream,
		StreamOptions:   req.StreamOptions,
		ReasoningEffort: req.ReasoningEffort,
	}

	// Build effective config with request overrides
	effectiveCfg := *p.cfg
	if req.ReasoningSummary != "" {
		effectiveCfg.ReasoningSummary = req.ReasoningSummary
	}
	if req.ReasoningCompat != "" {
		effectiveCfg.ReasoningCompat = req.ReasoningCompat
	}
	if req.TextVerbosity != "" {
		effectiveCfg.TextVerbosity = req.TextVerbosity
	}

	// Transform to ChatGPT Responses API request
	chatgptReq, err := TransformRequest(apiReq, instructions, &effectiveCfg)
	if err != nil {
		return nil, err
	}

	// Send request
	resp, err := p.client.SendRequest(ctx, chatgptReq)
	if err != nil {
		return nil, err
	}

	return &Stream{
		resp:            resp,
		reader:          sse.NewReader(resp.Body),
		state:           NewStreamState(),
		reasoningCompat: effectiveCfg.ReasoningCompat,
		stream:          req.Stream,
		includeUsage:    req.StreamOptions != nil && req.StreamOptions.IncludeUsage,
	}, nil
}

// Init performs initialization (e.g., prefetching instructions).
func (p *Provider) Init() error {
	return p.client.PrefetchInstructions()
}

// Start begins background tasks.
func (p *Provider) Start() {
	p.client.StartBackgroundRefresh()
}

// Close stops background tasks.
func (p *Provider) Close() {
	p.client.Close()
}

// RefreshModels forces a refresh of instruction files.
// For ChatGPT, this refreshes instructions rather than models (which are static).
func (p *Provider) RefreshModels(ctx context.Context) error {
	return p.client.RefreshInstructions(ctx)
}

// Stream implements the provider.Stream interface for ChatGPT responses.
type Stream struct {
	resp            *http.Response
	reader          *sse.Reader
	state           *StreamState
	reasoningCompat string // Effective reasoning compat mode for this stream
	stream          bool
	includeUsage    bool
	done            bool
	response        *api.ChatCompletionResponse
	err             error
	sentUsage       bool
	pendingChunks   []*api.ChatCompletionChunk // Buffer for multiple chunks from single event
}

// Next returns the next chunk.
func (s *Stream) Next() (*api.ChatCompletionChunk, error) {
	// Return buffered chunks first
	if len(s.pendingChunks) > 0 {
		chunk := s.pendingChunks[0]
		s.pendingChunks = s.pendingChunks[1:]
		return chunk, nil
	}

	if s.done {
		return nil, io.EOF
	}

	// Set reasoning compat mode
	s.state.SetReasoningCompat(s.reasoningCompat)

	// Check HTTP response status
	if s.resp.StatusCode != http.StatusOK {
		s.done = true
		body, _ := io.ReadAll(s.resp.Body)
		s.err = api.NewUpstreamError(s.resp.StatusCode, parseUpstreamError(body))
		return nil, s.err
	}

	for {
		event, err := s.reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				s.done = true
				// Build final response for non-streaming
				s.response = s.state.BuildNonStreamingResponse()

				// Send usage chunk if requested and not sent yet
				if s.includeUsage && !s.sentUsage {
					s.sentUsage = true
					if usageChunk := s.state.GetUsageChunk(); usageChunk != nil {
						return usageChunk, nil
					}
				}

				return nil, io.EOF
			}
			s.err = err
			return nil, err
		}

		chunks, err := s.state.ProcessEvent(event)
		if err != nil {
			s.err = err
			return nil, err
		}

		// Return first chunk and buffer the rest
		if len(chunks) > 0 {
			if len(chunks) > 1 {
				s.pendingChunks = append(s.pendingChunks, chunks[1:]...)
			}
			return chunks[0], nil
		}
		// Continue reading if no chunks produced
	}
}

// Response returns the accumulated non-streaming response.
func (s *Stream) Response() *api.ChatCompletionResponse {
	return s.response
}

// Err returns any error that occurred.
func (s *Stream) Err() error {
	// Prefer s.err as it may be an UpstreamError with status code
	if s.err != nil {
		return s.err
	}
	if errMsg := s.state.GetError(); errMsg != "" {
		return errors.New("upstream error: " + errMsg)
	}
	return nil
}

// Close releases resources.
func (s *Stream) Close() error {
	if s.resp != nil && s.resp.Body != nil {
		return s.resp.Body.Close()
	}
	return nil
}

// parseUpstreamError attempts to extract a meaningful error message from upstream response.
func parseUpstreamError(body []byte) string {
	var errResp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}

	if err := json.Unmarshal(body, &errResp); err == nil {
		if errResp.Error.Message != "" {
			return errResp.Error.Message
		}
		if errResp.Message != "" {
			return errResp.Message
		}
		if errResp.Detail != "" {
			return errResp.Detail
		}
	}

	bodyStr := string(body)
	if len(bodyStr) > 500 {
		bodyStr = bodyStr[:500] + "..."
	}
	return bodyStr
}
