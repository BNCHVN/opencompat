package chatgpt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/sse"
)

// intPtr returns a pointer to an int value.
func intPtr(i int) *int {
	return &i
}

// stringPtr returns a pointer to a string value.
func stringPtr(s string) *string {
	return &s
}

// buildWebSearchArgs safely builds JSON arguments for web search tool calls.
func buildWebSearchArgs(query string) string {
	if query == "" {
		return "{}"
	}
	args := map[string]string{"query": query}
	b, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// TransformRequest converts an OpenAI chat completion request to a ChatGPT Responses API request.
func TransformRequest(req *api.ChatCompletionRequest, instructions string, cfg *Config) (*ResponsesRequest, error) {
	// Normalize model name and extract effort suffix if present
	model, modelEffort := NormalizeModelNameWithEffort(req.Model)

	// Transform messages to input items
	input, err := transformMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	// Strip IDs from input items for stateless operation
	input = stripInputIDs(input)

	// Transform tools
	var tools []ToolDef
	if len(req.Tools) > 0 {
		tools = transformTools(req.Tools)
	}

	// Determine reasoning effort (priority: model suffix > request param > config)
	effort := cfg.ReasoningEffort
	if req.ReasoningEffort != "" {
		effort = req.ReasoningEffort
	}
	if modelEffort != "" {
		effort = modelEffort
	}
	effort = NormalizeReasoningEffort(model, effort)

	// Generate prompt cache key
	cacheKey := generateCacheKey(instructions, model)

	// Build the request
	respReq := &ResponsesRequest{
		Model:             model,
		Instructions:      instructions,
		Input:             input,
		Tools:             tools,
		ToolChoice:        req.ToolChoice,
		ParallelToolCalls: false,
		Store:             false,
		Stream:            true, // Always stream, we'll buffer for non-streaming
		Reasoning: &ReasoningConfig{
			Effort:  effort,
			Summary: cfg.ReasoningSummary,
		},
		Text: &TextConfig{
			Verbosity: cfg.TextVerbosity,
		},
		Include:        []string{"reasoning.encrypted_content"},
		PromptCacheKey: cacheKey,
	}

	return respReq, nil
}

// stripInputIDs removes IDs from input items for stateless operation.
// The ChatGPT backend requires store=false for stateless operation, which
// means we can't reference previous items by ID.
func stripInputIDs(input []InputItem) []InputItem {
	result := make([]InputItem, 0, len(input))
	for _, item := range input {
		// Skip item_reference types (server-side state lookup not available in stateless mode)
		if item.Type == "item_reference" {
			continue
		}

		// Copy item without ID (but keep CallID for function_call_output correlation)
		newItem := InputItem{
			Type:      item.Type,
			Role:      item.Role,
			Content:   item.Content,
			CallID:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Output:    item.Output,
			Status:    item.Status,
		}
		// Note: We intentionally don't copy item.ID to strip it
		result = append(result, newItem)
	}
	return result
}

func transformMessages(messages []api.Message) ([]InputItem, error) {
	var input []InputItem

	// First pass: extract system messages and convert to user message
	// The ChatGPT Responses API doesn't support system messages directly,
	// so we convert them to a user message at the start of the conversation
	var systemContent string
	var nonSystemMessages []api.Message
	for _, msg := range messages {
		if msg.Role == "system" {
			content := msg.GetContentString()
			if content != "" {
				if systemContent != "" {
					systemContent += "\n"
				}
				systemContent += content
			}
		} else {
			nonSystemMessages = append(nonSystemMessages, msg)
		}
	}

	// Prepend system content as a user message if present
	if systemContent != "" {
		contentJSON, _ := json.Marshal(systemContent)
		input = append(input, InputItem{
			Type:    "message",
			Role:    "user",
			Content: contentJSON,
		})
	}

	// Second pass: process non-system messages
	for _, msg := range nonSystemMessages {
		// Handle tool results - create function_call_output WITHOUT role field
		// The Responses API expects: {"type": "function_call_output", "call_id": "...", "output": "..."}
		// NOT: {"type": "function_call_output", "role": "tool", ...}
		if msg.Role == "tool" {
			input = append(input, InputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.GetContentString(),
			})
			continue
		}

		item := InputItem{
			Type: "message",
			Role: msg.Role,
		}

		// Handle assistant messages with tool calls
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// First add the assistant message if there's content
			// Check for string content first, then array content
			content := msg.GetContentString()
			if content != "" {
				item.Content, _ = json.Marshal(content)
				input = append(input, item)
			} else {
				// Try array content (multimodal)
				parts := msg.GetContentParts()
				if len(parts) > 0 {
					// Convert to content blocks
					var blocks []ContentBlock
					for _, part := range parts {
						switch part.Type {
						case "text":
							blocks = append(blocks, ContentBlock{
								Type: "input_text",
								Text: part.Text,
							})
						case "image_url":
							if part.ImageURL != nil {
								block := ContentBlock{
									Type:     "input_image",
									ImageURL: part.ImageURL.URL,
								}
								if part.ImageURL.Detail != "" {
									block.Detail = part.ImageURL.Detail
								}
								blocks = append(blocks, block)
							}
						}
					}
					if len(blocks) > 0 {
						item.Content, _ = json.Marshal(blocks)
						input = append(input, item)
					}
				}
			}

			// Then add each function call
			for _, tc := range msg.ToolCalls {
				fcItem := InputItem{
					Type:      "function_call",
					ID:        tc.ID,
					CallID:    tc.ID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
					Status:    "completed",
				}
				input = append(input, fcItem)
			}
			continue
		}

		// Handle regular messages (string or multimodal content)
		parts := msg.GetContentParts()
		if len(parts) == 0 {
			// Empty message
			item.Content, _ = json.Marshal("")
			input = append(input, item)
			continue
		}

		// Check if it's a simple text message
		if len(parts) == 1 && parts[0].Type == "text" {
			item.Content, _ = json.Marshal(parts[0].Text)
			input = append(input, item)
			continue
		}

		// Multimodal content - transform to content blocks
		var blocks []ContentBlock
		for _, part := range parts {
			switch part.Type {
			case "text":
				blocks = append(blocks, ContentBlock{
					Type: "input_text",
					Text: part.Text,
				})
			case "image_url":
				if part.ImageURL != nil {
					block := ContentBlock{
						Type:     "input_image",
						ImageURL: part.ImageURL.URL,
					}
					if part.ImageURL.Detail != "" {
						block.Detail = part.ImageURL.Detail
					}
					blocks = append(blocks, block)
				}
			default:
				// Log warning for unknown content part types
				slog.Warn("unknown content part type ignored", "type", part.Type)
			}
		}

		if len(blocks) > 0 {
			item.Content, _ = json.Marshal(blocks)
		}
		input = append(input, item)
	}

	return input, nil
}

func transformTools(tools []api.Tool) []ToolDef {
	var result []ToolDef
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		result = append(result, ToolDef{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
			Strict:      t.Function.Strict,
		})
	}
	return result
}

func generateCacheKey(instructions, model string) string {
	h := sha256.New()
	h.Write([]byte(instructions))
	h.Write([]byte(model))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// StreamState tracks state during SSE streaming.
type StreamState struct {
	ResponseID            string
	Model                 string
	Created               int64
	CurrentContent        string
	ReasoningSummary      string
	ReasoningFull         string
	ToolCalls             map[int]*api.ToolCall // indexed by output_index
	WebSearchCalls        map[string]int        // call_id -> output_index for web search
	WebSearchInitialSent  map[string]bool       // Tracks which web search calls sent initial chunk
	FinishReason          string
	IncompleteReason      string // "max_output_tokens", "content_filter", etc.
	Usage                 *api.Usage
	ReasoningCompat       string // "none", "think-tags", "o3", "legacy"
	ThinkTagOpen          bool
	ThinkTagClosed        bool
	SawOutput             bool
	SentStopChunk         bool
	SentToolCallFinish    bool // Prevents duplicate tool_calls finish chunks
	PendingSummaryNewline bool
	ErrorMessage          string
}

// NewStreamState creates a new stream state.
func NewStreamState() *StreamState {
	return &StreamState{
		ToolCalls:            make(map[int]*api.ToolCall),
		WebSearchCalls:       make(map[string]int),
		WebSearchInitialSent: make(map[string]bool),
		ReasoningCompat:      "none", // Default to none
	}
}

// SetReasoningCompat sets the reasoning compatibility mode.
func (s *StreamState) SetReasoningCompat(mode string) {
	s.ReasoningCompat = mode
}

// ProcessEvent processes an SSE event and returns OpenAI chunks if applicable.
// May return multiple chunks for complex events.
func (s *StreamState) ProcessEvent(event *sse.Event) ([]*api.ChatCompletionChunk, error) {
	switch event.Event {
	case EventResponseCreated:
		var data ResponseCreatedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}
		s.ResponseID = data.Response.ID
		s.Model = data.Response.Model
		s.Created = currentTimestamp()

		// Send initial chunk with role
		return []*api.ChatCompletionChunk{{
			ID:      s.ResponseID,
			Object:  "chat.completion.chunk",
			Created: s.Created,
			Model:   s.Model,
			Choices: []api.Choice{{
				Index: 0,
				Delta: &api.Delta{Role: "assistant"},
			}},
		}}, nil

	case EventResponseOutputTextDelta:
		var data TextDeltaData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		var chunks []*api.ChatCompletionChunk

		// Close think tag if open and we're getting output
		if s.ReasoningCompat == "think-tags" && s.ThinkTagOpen && !s.ThinkTagClosed {
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{Content: "</think>"},
				}},
			})
			s.ThinkTagOpen = false
			s.ThinkTagClosed = true
		}

		s.SawOutput = true
		s.CurrentContent += data.Delta

		chunks = append(chunks, &api.ChatCompletionChunk{
			ID:      s.ResponseID,
			Object:  "chat.completion.chunk",
			Created: s.Created,
			Model:   s.Model,
			Choices: []api.Choice{{
				Index: 0,
				Delta: &api.Delta{Content: data.Delta},
			}},
		})

		return chunks, nil

	case EventResponseOutputTextDone:
		// Text completion marker - send stop if not already sent
		if !s.SentStopChunk {
			s.SentStopChunk = true
			return []*api.ChatCompletionChunk{{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index:        0,
					Delta:        &api.Delta{},
					FinishReason: stringPtr("stop"),
				}},
			}}, nil
		}
		return nil, nil

	case EventResponseReasoningSummaryPartAdded:
		// New reasoning paragraph marker
		if s.ReasoningCompat == "think-tags" || s.ReasoningCompat == "o3" {
			if s.ReasoningSummary != "" || s.ReasoningFull != "" {
				s.PendingSummaryNewline = true
			}
		}
		return nil, nil

	case EventResponseReasoningSummaryTextDelta, EventResponseReasoningTextDelta:
		var data ReasoningTextDeltaData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		// Track reasoning content
		if event.Event == EventResponseReasoningSummaryTextDelta {
			s.ReasoningSummary += data.Delta
		} else {
			s.ReasoningFull += data.Delta
		}

		// Handle based on compat mode
		switch s.ReasoningCompat {
		case "none":
			// Don't emit reasoning
			return nil, nil

		case "think-tags":
			var chunks []*api.ChatCompletionChunk

			// Open think tag if not already open
			if !s.ThinkTagOpen && !s.ThinkTagClosed {
				chunks = append(chunks, &api.ChatCompletionChunk{
					ID:      s.ResponseID,
					Object:  "chat.completion.chunk",
					Created: s.Created,
					Model:   s.Model,
					Choices: []api.Choice{{
						Index: 0,
						Delta: &api.Delta{Content: "<think>"},
					}},
				})
				s.ThinkTagOpen = true
			}

			// Add newline between paragraphs
			if s.ThinkTagOpen && !s.ThinkTagClosed && s.PendingSummaryNewline {
				chunks = append(chunks, &api.ChatCompletionChunk{
					ID:      s.ResponseID,
					Object:  "chat.completion.chunk",
					Created: s.Created,
					Model:   s.Model,
					Choices: []api.Choice{{
						Index: 0,
						Delta: &api.Delta{Content: "\n"},
					}},
				})
				s.PendingSummaryNewline = false
			}

			// Emit reasoning content
			if s.ThinkTagOpen && !s.ThinkTagClosed {
				chunks = append(chunks, &api.ChatCompletionChunk{
					ID:      s.ResponseID,
					Object:  "chat.completion.chunk",
					Created: s.Created,
					Model:   s.Model,
					Choices: []api.Choice{{
						Index: 0,
						Delta: &api.Delta{Content: data.Delta},
					}},
				})
			}

			return chunks, nil

		case "o3":
			var chunks []*api.ChatCompletionChunk

			// Add newline between paragraphs
			if s.PendingSummaryNewline {
				chunks = append(chunks, &api.ChatCompletionChunk{
					ID:      s.ResponseID,
					Object:  "chat.completion.chunk",
					Created: s.Created,
					Model:   s.Model,
					Choices: []api.Choice{{
						Index: 0,
						Delta: &api.Delta{
							Reasoning: &api.ReasoningOutput{
								Content: []api.ReasoningContent{{Type: "text", Text: "\n"}},
							},
						},
					}},
				})
				s.PendingSummaryNewline = false
			}

			// Emit reasoning in o3 format
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{
						Reasoning: &api.ReasoningOutput{
							Content: []api.ReasoningContent{{Type: "text", Text: data.Delta}},
						},
					},
				}},
			})
			return chunks, nil

		case "legacy":
			// Emit as separate fields - only for summary events
			// Skip non-summary reasoning events to avoid empty deltas
			if event.Event != EventResponseReasoningSummaryTextDelta {
				return nil, nil
			}
			return []*api.ChatCompletionChunk{{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{ReasoningSummary: data.Delta},
				}},
			}}, nil
		}

		return nil, nil

	case EventResponseFunctionCallArgumentsDelta:
		var data FunctionArgumentsDeltaData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		// Get or create tool call
		tc, ok := s.ToolCalls[data.OutputIndex]
		if !ok {
			// Tool call should have been created in output_item.added
			return nil, nil
		}
		tc.Function.Arguments += data.Delta

		return []*api.ChatCompletionChunk{{
			ID:      s.ResponseID,
			Object:  "chat.completion.chunk",
			Created: s.Created,
			Model:   s.Model,
			Choices: []api.Choice{{
				Index: 0,
				Delta: &api.Delta{
					ToolCalls: []api.ToolCall{{
						Index:    intPtr(data.OutputIndex),
						Function: api.FunctionCall{Arguments: data.Delta},
					}},
				},
			}},
		}}, nil

	case EventResponseOutputItemAdded:
		var data OutputItemAddedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		if data.Item.Type == "function_call" {
			tc := &api.ToolCall{
				ID:   data.Item.CallID,
				Type: "function",
				Function: api.FunctionCall{
					Name: data.Item.Name,
				},
			}
			s.ToolCalls[data.OutputIndex] = tc

			// Send initial tool call chunk
			return []*api.ChatCompletionChunk{{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{
						ToolCalls: []api.ToolCall{{
							Index: intPtr(data.OutputIndex),
							ID:    tc.ID,
							Type:  "function",
							Function: api.FunctionCall{
								Name: tc.Function.Name,
							},
						}},
					},
				}},
			}}, nil
		}

		// Handle web_search_call type
		if data.Item.Type == "web_search_call" {
			callID := data.Item.CallID
			if callID == "" {
				callID = data.Item.ID
			}

			// Use output_index for consistent tool call indexing
			// Also track by callID for correlation in other events
			s.WebSearchCalls[callID] = data.OutputIndex

			tc := &api.ToolCall{
				ID:   callID,
				Type: "function",
				Function: api.FunctionCall{
					Name:      "web_search",
					Arguments: "{}",
				},
			}
			s.ToolCalls[data.OutputIndex] = tc

			// Mark as initial chunk sent
			s.WebSearchInitialSent[callID] = true

			return []*api.ChatCompletionChunk{{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{
						ToolCalls: []api.ToolCall{{
							Index: intPtr(data.OutputIndex),
							ID:    callID,
							Type:  "function",
							Function: api.FunctionCall{
								Name: "web_search",
							},
						}},
					},
				}},
			}}, nil
		}

		return nil, nil

	case EventResponseOutputItemDone:
		var data OutputItemDoneData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		// Handle completed web_search_call
		if data.Item.Type == "web_search_call" {
			callID := data.Item.CallID
			if callID == "" {
				callID = data.Item.ID
			}

			// Get index from output_index (consistent with output_item.added)
			idx := data.OutputIndex
			// Update map if not already present
			if _, exists := s.WebSearchCalls[callID]; !exists {
				s.WebSearchCalls[callID] = idx
			}

			// Build arguments from item
			args := "{}"
			if data.Item.Arguments != "" {
				args = data.Item.Arguments
			}

			// Update stored tool call
			if tc, ok := s.ToolCalls[data.OutputIndex]; ok {
				tc.Function.Arguments = args
			}

			// Only send arguments delta (ID/Type/Name already sent in output_item.added)
			chunks := []*api.ChatCompletionChunk{{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{
						ToolCalls: []api.ToolCall{{
							Index:    intPtr(idx),
							Function: api.FunctionCall{Arguments: args},
						}},
					},
				}},
			}}

			// Send finish chunk only once
			if !s.SentToolCallFinish {
				s.SentToolCallFinish = true
				chunks = append(chunks, &api.ChatCompletionChunk{
					ID:      s.ResponseID,
					Object:  "chat.completion.chunk",
					Created: s.Created,
					Model:   s.Model,
					Choices: []api.Choice{{
						Index:        0,
						Delta:        &api.Delta{},
						FinishReason: stringPtr("tool_calls"),
					}},
				})
			}

			return chunks, nil
		}

		return nil, nil

	case EventWebSearchCallSearching, EventWebSearchCallInProgress, EventWebSearchCallCompleted:
		// Handle web search call events
		var data WebSearchCallData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		callID := data.ItemID
		if callID == "" && data.Item != nil {
			callID = data.Item.CallID
			if callID == "" {
				callID = data.Item.ID
			}
		}
		if callID == "" {
			return nil, nil
		}

		// Get index: prefer OutputIndex from event, fallback to map lookup
		idx, exists := s.WebSearchCalls[callID]
		if !exists {
			// Use OutputIndex if provided, otherwise skip (should have been added via output_item.added)
			if data.OutputIndex > 0 {
				idx = data.OutputIndex
				s.WebSearchCalls[callID] = idx
			} else {
				// No index available, skip this event
				return nil, nil
			}
		}

		// Build arguments safely using JSON marshaling
		query := ""
		if data.Query != "" {
			query = data.Query
		} else if data.Params != nil && data.Params.Query != "" {
			query = data.Params.Query
		} else if data.Item != nil && data.Item.Query != "" {
			query = data.Item.Query
		}
		args := buildWebSearchArgs(query)

		// Only send arguments delta (ID/Type/Name already sent in output_item.added)
		chunks := []*api.ChatCompletionChunk{{
			ID:      s.ResponseID,
			Object:  "chat.completion.chunk",
			Created: s.Created,
			Model:   s.Model,
			Choices: []api.Choice{{
				Index: 0,
				Delta: &api.Delta{
					ToolCalls: []api.ToolCall{{
						Index:    intPtr(idx),
						Function: api.FunctionCall{Arguments: args},
					}},
				},
			}},
		}}

		// Send finish if completed (only once)
		if event.Event == EventWebSearchCallCompleted && !s.SentToolCallFinish {
			s.SentToolCallFinish = true
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index:        0,
					Delta:        &api.Delta{},
					FinishReason: stringPtr("tool_calls"),
				}},
			})
		}

		return chunks, nil

	case EventResponseCompleted:
		var data ResponseCompletedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		var chunks []*api.ChatCompletionChunk

		// Close think tag if still open
		if s.ReasoningCompat == "think-tags" && s.ThinkTagOpen && !s.ThinkTagClosed {
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{Content: "</think>"},
				}},
			})
			s.ThinkTagOpen = false
			s.ThinkTagClosed = true
		}

		// Determine finish reason
		finishReason := "stop"
		if len(s.ToolCalls) > 0 {
			finishReason = "tool_calls"
		}
		s.FinishReason = finishReason

		// Extract usage
		if data.Response.Usage != nil {
			s.Usage = &api.Usage{
				PromptTokens:     data.Response.Usage.InputTokens,
				CompletionTokens: data.Response.Usage.OutputTokens,
				TotalTokens:      data.Response.Usage.TotalTokens,
			}
		}

		// Send final chunk if not already sent
		if !s.SentStopChunk {
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index:        0,
					Delta:        &api.Delta{},
					FinishReason: stringPtr(finishReason),
				}},
			})
			s.SentStopChunk = true
		}

		return chunks, nil

	case EventResponseFailed:
		var data ResponseFailedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}
		s.FinishReason = "error"
		if data.Response.Error != nil {
			s.ErrorMessage = data.Response.Error.Message
		}
		return nil, nil

	case EventError:
		var data ErrorEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}
		s.FinishReason = "error"
		s.ErrorMessage = data.Message
		return nil, nil

	case EventResponseInProgress, EventResponseQueued:
		// These are status updates, no chunks to emit
		return nil, nil

	case EventResponseIncomplete:
		// Response was cut short (max tokens, content filter, etc.)
		var data ResponseIncompleteData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, err
		}

		var chunks []*api.ChatCompletionChunk

		// Close think tag if still open
		if s.ReasoningCompat == "think-tags" && s.ThinkTagOpen && !s.ThinkTagClosed {
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index: 0,
					Delta: &api.Delta{Content: "</think>"},
				}},
			})
			s.ThinkTagOpen = false
			s.ThinkTagClosed = true
		}

		// Map incomplete reason to finish reason
		finishReason := "length" // Default for max_output_tokens
		if data.Response.IncompleteReason == "content_filter" {
			finishReason = "content_filter"
		}
		s.FinishReason = finishReason

		// Extract usage if present
		if data.Response.Usage != nil {
			s.Usage = &api.Usage{
				PromptTokens:     data.Response.Usage.InputTokens,
				CompletionTokens: data.Response.Usage.OutputTokens,
				TotalTokens:      data.Response.Usage.TotalTokens,
			}
		}

		// Send final chunk
		if !s.SentStopChunk {
			chunks = append(chunks, &api.ChatCompletionChunk{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index:        0,
					Delta:        &api.Delta{},
					FinishReason: stringPtr(finishReason),
				}},
			})
			s.SentStopChunk = true
		}

		return chunks, nil

	case EventResponseCancelled:
		s.FinishReason = "stop"
		if !s.SentStopChunk {
			s.SentStopChunk = true
			return []*api.ChatCompletionChunk{{
				ID:      s.ResponseID,
				Object:  "chat.completion.chunk",
				Created: s.Created,
				Model:   s.Model,
				Choices: []api.Choice{{
					Index:        0,
					Delta:        &api.Delta{},
					FinishReason: stringPtr("stop"),
				}},
			}}, nil
		}
		return nil, nil

	case EventResponseContentPartAdded, EventResponseContentPartDone:
		// Content part events are informational, actual content comes through text delta events
		return nil, nil

	case EventResponseReasoningSummaryPartDone, EventResponseReasoningTextDone,
		EventResponseReasoningSummaryTextDone, EventResponseFunctionCallArgumentsDone:
		// These are completion markers for their respective delta events
		// No additional chunks needed as the content is already streamed
		return nil, nil

	case EventFileSearchCallSearching, EventFileSearchCallInProgress, EventFileSearchCallCompleted,
		EventMCPCallInProgress, EventMCPCallCompleted, EventMCPCallFailed:
		// File search and MCP events - currently treated as informational
		// Could be expanded to emit tool call chunks if needed
		return nil, nil
	}

	return nil, nil
}

// GetUsageChunk returns a chunk with usage information for streaming.
func (s *StreamState) GetUsageChunk() *api.ChatCompletionChunk {
	if s.Usage == nil {
		return nil
	}

	// Generate system fingerprint from response ID
	systemFingerprint := ""
	if len(s.ResponseID) > 8 {
		systemFingerprint = "fp_" + s.ResponseID[len(s.ResponseID)-8:]
	}

	return &api.ChatCompletionChunk{
		ID:                s.ResponseID,
		Object:            "chat.completion.chunk",
		Created:           s.Created,
		Model:             s.Model,
		SystemFingerprint: systemFingerprint,
		Choices:           []api.Choice{}, // Empty choices array for usage-only chunk
		Usage:             s.Usage,
	}
}

// GetError returns any error message from the stream.
func (s *StreamState) GetError() string {
	return s.ErrorMessage
}

// BuildNonStreamingResponse builds a complete ChatCompletionResponse from state.
func (s *StreamState) BuildNonStreamingResponse() *api.ChatCompletionResponse {
	msg := &api.Message{
		Role: "assistant",
	}

	// Build combined reasoning text (used by multiple modes)
	reasoningText := s.ReasoningSummary
	if s.ReasoningFull != "" {
		if reasoningText != "" {
			reasoningText += "\n" // Use single newline for consistency with streaming
		}
		reasoningText += s.ReasoningFull
	}

	// Build content with reasoning based on compat mode
	content := s.CurrentContent
	switch s.ReasoningCompat {
	case "think-tags":
		if reasoningText != "" {
			content = "<think>" + reasoningText + "</think>" + content
		}
	case "o3":
		if reasoningText != "" {
			msg.Reasoning = &api.ReasoningOutput{
				Content: []api.ReasoningContent{{Type: "text", Text: reasoningText}},
			}
		}
	case "legacy":
		if s.ReasoningSummary != "" {
			msg.ReasoningSummary = s.ReasoningSummary
		}
	}
	msg.SetContentString(content)

	// Add tool calls if any (sorted by output index)
	// Note: For non-streaming responses, tool calls should NOT have Index field
	if len(s.ToolCalls) > 0 {
		// Find max index to iterate correctly
		maxIdx := 0
		for idx := range s.ToolCalls {
			if idx > maxIdx {
				maxIdx = idx
			}
		}
		for i := 0; i <= maxIdx; i++ {
			if tc, ok := s.ToolCalls[i]; ok {
				// Copy tool call without Index for non-streaming response
				msg.ToolCalls = append(msg.ToolCalls, api.ToolCall{
					ID:       tc.ID,
					Type:     tc.Type,
					Function: tc.Function,
				})
			}
		}
	}

	// Generate system fingerprint from response ID
	systemFingerprint := ""
	if len(s.ResponseID) > 8 {
		systemFingerprint = "fp_" + s.ResponseID[len(s.ResponseID)-8:]
	}

	return &api.ChatCompletionResponse{
		ID:                s.ResponseID,
		Object:            "chat.completion",
		Created:           s.Created,
		Model:             s.Model,
		SystemFingerprint: systemFingerprint,
		Choices: []api.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: stringPtr(s.FinishReason),
		}},
		Usage: s.Usage,
	}
}

func currentTimestamp() int64 {
	return time.Now().Unix()
}
