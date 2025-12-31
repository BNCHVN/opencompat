package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/config"
	"github.com/edgard/opencompat/internal/provider"
)

// Maximum request body size (10MB)
const maxRequestBodySize = 10 * 1024 * 1024

// validRoles defines the valid message roles for OpenAI API
var validRoles = map[string]bool{
	"system":    true,
	"user":      true,
	"assistant": true,
	"tool":      true,
}

// logIgnoredParameters logs warnings for parameters that are accepted but ignored.
// providerID is used to determine which parameters are actually ignored (some providers support them).
func logIgnoredParameters(requestID string, req *api.ChatCompletionRequest, providerID string) {
	var ignored []string

	// These parameters are ignored by all providers
	if req.N != nil && *req.N != 1 {
		ignored = append(ignored, "n")
	}
	if req.LogitBias != nil {
		ignored = append(ignored, "logit_bias")
	}
	if req.Seed != nil {
		ignored = append(ignored, "seed")
	}
	if req.User != "" {
		ignored = append(ignored, "user")
	}

	// These parameters are only ignored by ChatGPT (Copilot supports them)
	if providerID != "copilot" {
		if req.Temperature != nil {
			ignored = append(ignored, "temperature")
		}
		if req.TopP != nil {
			ignored = append(ignored, "top_p")
		}
		if req.Stop != nil {
			ignored = append(ignored, "stop")
		}
		if req.MaxTokens != nil {
			ignored = append(ignored, "max_tokens")
		}
		if req.MaxCompletionTokens != nil {
			ignored = append(ignored, "max_completion_tokens")
		}
		if req.PresencePenalty != nil {
			ignored = append(ignored, "presence_penalty")
		}
		if req.FrequencyPenalty != nil {
			ignored = append(ignored, "frequency_penalty")
		}
		if req.ResponseFormat != nil {
			ignored = append(ignored, "response_format")
		}
		if req.ParallelToolCalls != nil {
			ignored = append(ignored, "parallel_tool_calls")
		}
	}

	// reasoning_effort is only supported by ChatGPT (ignored by Copilot)
	if providerID == "copilot" && req.ReasoningEffort != "" {
		ignored = append(ignored, "reasoning_effort")
	}

	if len(ignored) > 0 {
		slog.Warn("ignoring unsupported parameters",
			"request_id", requestID,
			"params", strings.Join(ignored, ", "),
		)
	}
}

// writeStreamError writes an appropriate error response, checking for UpstreamError first.
func writeStreamError(w http.ResponseWriter, err error, prefix string) {
	var upstreamErr *api.UpstreamError
	if errors.As(err, &upstreamErr) {
		api.WriteUpstreamError(w, upstreamErr)
		return
	}
	api.WriteServerError(w, prefix+err.Error())
}

// formatErrorForSSE formats an error message for SSE streams, including status code if available.
func formatErrorForSSE(err error, prefix string) string {
	var upstreamErr *api.UpstreamError
	if errors.As(err, &upstreamErr) {
		return fmt.Sprintf("%s (status %d): %s", prefix, upstreamErr.StatusCode, upstreamErr.Message)
	}
	return prefix + ": " + err.Error()
}

// Handlers holds the HTTP handlers and their dependencies.
type Handlers struct {
	registry *provider.Registry
	cfg      *config.Config
}

// NewHandlers creates a new handlers instance.
func NewHandlers(registry *provider.Registry, cfg *config.Config) *Handlers {
	return &Handlers{
		registry: registry,
		cfg:      cfg,
	}
}

// Health handles GET /health
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteMethodNotAllowed(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Models handles GET /v1/models
func (h *Handlers) Models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.WriteMethodNotAllowed(w)
		return
	}

	// Get all models from all active providers (with provider prefix)
	models := h.registry.AllModels()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.ModelsResponse{
		Object: "list",
		Data:   models,
	})
}

// ChatCompletions handles POST /v1/chat/completions
func (h *Handlers) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteMethodNotAllowed(w)
		return
	}

	// Get request ID from context (set by middleware)
	requestID := GetRequestID(r.Context())

	// Limit request body size to prevent DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	// Parse request
	var req api.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			api.WriteBadRequest(w, "Request body too large (max 10MB)")
			return
		}
		api.WriteBadRequest(w, "Invalid JSON: "+err.Error())
		return
	}

	// Validate model
	if req.Model == "" {
		api.WriteBadRequestWithParam(w, "model is required", "model")
		return
	}

	// Get provider for the model (model must include provider prefix)
	p, modelID, err := h.registry.GetProvider(req.Model)
	if err != nil {
		// Check if it's a "provider requires login" error
		if strings.Contains(err.Error(), "requires login") {
			api.WriteError(w, http.StatusUnauthorized, api.ErrorTypeAuthentication, err.Error(), nil, nil)
			return
		}
		// Check if it's a missing provider prefix
		if strings.Contains(err.Error(), "must include provider prefix") {
			api.WriteBadRequestWithParam(w, err.Error(), "model")
			return
		}
		api.WriteModelNotFound(w, req.Model)
		return
	}

	// Log warnings for ignored parameters (after we know the provider)
	logIgnoredParameters(requestID, &req, p.ID())

	// Check if model is supported by the provider
	if !h.registry.IsModelSupported(req.Model) {
		api.WriteModelNotFound(w, req.Model)
		return
	}

	// Validate messages
	if len(req.Messages) == 0 {
		api.WriteBadRequestWithParam(w, "messages is required", "messages")
		return
	}

	// Validate each message
	for i, msg := range req.Messages {
		// Validate role
		if !validRoles[msg.Role] {
			api.WriteBadRequestWithParam(w,
				fmt.Sprintf("Invalid role '%s'. Must be one of: system, user, assistant, tool", msg.Role),
				fmt.Sprintf("messages[%d].role", i))
			return
		}

		// Validate tool messages have tool_call_id
		if msg.Role == "tool" && msg.ToolCallID == "" {
			api.WriteBadRequestWithParam(w,
				"Tool messages must include tool_call_id",
				fmt.Sprintf("messages[%d].tool_call_id", i))
			return
		}
	}

	// Build provider request (provider handles model normalization internally)
	providerReq := &provider.ChatCompletionRequest{
		Model:               modelID,
		Messages:            req.Messages,
		Tools:               req.Tools,
		ToolChoice:          req.ToolChoice,
		Stream:              req.Stream,
		StreamOptions:       req.StreamOptions,
		ReasoningEffort:     req.ReasoningEffort,
		ReasoningSummary:    r.Header.Get("X-Reasoning-Summary"),
		ReasoningCompat:     r.Header.Get("X-Reasoning-Compat"),
		TextVerbosity:       r.Header.Get("X-Text-Verbosity"),
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		MaxTokens:           req.MaxTokens,
		MaxCompletionTokens: req.MaxCompletionTokens,
		Stop:                req.Stop,
		PresencePenalty:     req.PresencePenalty,
		FrequencyPenalty:    req.FrequencyPenalty,
		ResponseFormat:      req.ResponseFormat,
		ParallelToolCalls:   req.ParallelToolCalls,
	}

	// Send request to provider
	stream, err := p.ChatCompletion(r.Context(), providerReq)
	if err != nil {
		api.WriteServerError(w, "Failed to send request: "+err.Error())
		return
	}
	defer func() { _ = stream.Close() }()

	// Handle streaming vs non-streaming
	if req.Stream {
		h.handleStreaming(w, stream)
	} else {
		h.handleNonStreaming(w, stream)
	}
}

func (h *Handlers) handleStreaming(w http.ResponseWriter, stream provider.Stream) {
	var sseWriter *SSEWriter
	var streamErr error

	for {
		chunk, err := stream.Next()
		if err != nil {
			if err != io.EOF {
				streamErr = err
			}
			break
		}

		// Initialize SSE writer on first successful chunk
		if sseWriter == nil {
			var initErr error
			sseWriter, initErr = NewSSEWriter(w)
			if initErr != nil {
				api.WriteServerError(w, initErr.Error())
				return
			}
		}

		if err := sseWriter.WriteChunk(chunk); err != nil {
			// Client disconnected
			return
		}
	}

	// If no chunks were sent, we can still return a proper HTTP error
	if sseWriter == nil {
		// Prefer streamErr if set, otherwise check stream.Err()
		err := streamErr
		if err == nil {
			err = stream.Err()
		}
		if err != nil {
			writeStreamError(w, err, "Stream error: ")
			return
		}
		api.WriteServerError(w, "No response received from upstream")
		return
	}

	// For errors after streaming started, write error to SSE stream.
	// streamErr is set when Next() returns a non-EOF error.
	// stream.Err() may return additional errors from SSE event processing (e.g., response.failed).
	if streamErr != nil {
		_ = sseWriter.WriteError(formatErrorForSSE(streamErr, "Stream error"))
	} else if err := stream.Err(); err != nil {
		_ = sseWriter.WriteError(formatErrorForSSE(err, "Upstream error"))
	}

	_ = sseWriter.WriteDone()
}

func (h *Handlers) handleNonStreaming(w http.ResponseWriter, stream provider.Stream) {
	// Consume the stream to build the response
	for {
		_, err := stream.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			writeStreamError(w, err, "Stream read error: ")
			return
		}
	}

	// Check for stream error
	if err := stream.Err(); err != nil {
		writeStreamError(w, err, "Upstream error: ")
		return
	}

	// Get the accumulated response
	response := stream.Response()
	if response == nil || response.ID == "" {
		api.WriteServerError(w, "No response received from upstream")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
