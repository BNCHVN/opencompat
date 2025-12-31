package copilot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/sse"
)

// Stream implements the provider.Stream interface for Copilot responses.
// Copilot uses standard OpenAI SSE format, so this is simpler than ChatGPT's
// Responses API transformation.
type Stream struct {
	resp         *http.Response
	reader       *sse.Reader
	stream       bool
	includeUsage bool
	done         bool
	response     *api.ChatCompletionResponse
	err          error
	sentUsage    bool
	lastChunk    *api.ChatCompletionChunk // For capturing usage
}

// NewStream creates a new stream from an HTTP response.
func NewStream(resp *http.Response, stream, includeUsage bool) *Stream {
	return &Stream{
		resp:         resp,
		reader:       sse.NewReader(resp.Body),
		stream:       stream,
		includeUsage: includeUsage,
	}
}

// Next returns the next chunk from the stream.
func (s *Stream) Next() (*api.ChatCompletionChunk, error) {
	if s.done {
		return nil, io.EOF
	}

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

				// Send final usage chunk if requested and we have usage data
				if s.includeUsage && !s.sentUsage && s.lastChunk != nil && s.lastChunk.Usage != nil {
					s.sentUsage = true
					return &api.ChatCompletionChunk{
						ID:      s.lastChunk.ID,
						Object:  "chat.completion.chunk",
						Created: s.lastChunk.Created,
						Model:   s.lastChunk.Model,
						Choices: []api.Choice{},
						Usage:   s.lastChunk.Usage,
					}, nil
				}

				return nil, io.EOF
			}
			s.err = err
			return nil, err
		}

		// Skip events with no data
		if len(event.Data) == 0 {
			continue
		}

		// Parse chunk
		var chunk api.ChatCompletionChunk
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			// Skip malformed events
			continue
		}

		// Normalize chunk: ensure Object is set (Copilot may send empty object field)
		if chunk.Object == "" {
			chunk.Object = "chat.completion.chunk"
		}

		// Store for potential usage extraction
		if chunk.Usage != nil {
			s.lastChunk = &chunk
		}

		// Accumulate for non-streaming response
		if !s.stream {
			s.accumulateResponse(&chunk)
		}

		return &chunk, nil
	}
}

// accumulateResponse builds the non-streaming response from chunks.
func (s *Stream) accumulateResponse(chunk *api.ChatCompletionChunk) {
	if s.response == nil {
		s.response = &api.ChatCompletionResponse{
			ID:                chunk.ID,
			Object:            "chat.completion",
			Created:           chunk.Created,
			Model:             chunk.Model,
			SystemFingerprint: chunk.SystemFingerprint,
			Choices:           make([]api.Choice, len(chunk.Choices)),
		}
		for i := range chunk.Choices {
			s.response.Choices[i] = api.Choice{
				Index:   chunk.Choices[i].Index,
				Message: &api.Message{Role: "assistant"},
			}
		}
	}

	// Update response metadata from subsequent chunks if initially empty
	// (Copilot may send an empty first chunk followed by chunks with actual data)
	if s.response.ID == "" && chunk.ID != "" {
		s.response.ID = chunk.ID
	}
	if s.response.Model == "" && chunk.Model != "" {
		s.response.Model = chunk.Model
	}
	if s.response.Created == 0 && chunk.Created != 0 {
		s.response.Created = chunk.Created
	}
	if s.response.SystemFingerprint == "" && chunk.SystemFingerprint != "" {
		s.response.SystemFingerprint = chunk.SystemFingerprint
	}

	// Accumulate content from deltas
	for _, cc := range chunk.Choices {
		// Expand choices array if needed (handles empty first chunk from Copilot)
		for len(s.response.Choices) <= cc.Index {
			s.response.Choices = append(s.response.Choices, api.Choice{
				Index:   len(s.response.Choices),
				Message: &api.Message{Role: "assistant"},
			})
		}

		choice := &s.response.Choices[cc.Index]
		if choice.Message == nil {
			choice.Message = &api.Message{Role: "assistant"}
		}
		if cc.Delta != nil {
			if cc.Delta.Content != "" {
				// Append to existing content
				existingContent := choice.Message.GetContentString()
				choice.Message.SetContentString(existingContent + cc.Delta.Content)
			}
			if cc.Delta.Role != "" {
				choice.Message.Role = cc.Delta.Role
			}
			// Handle tool calls
			if len(cc.Delta.ToolCalls) > 0 {
				for _, tc := range cc.Delta.ToolCalls {
					idx := 0
					if tc.Index != nil {
						idx = *tc.Index
					}
					// Ensure we have enough tool calls
					for len(choice.Message.ToolCalls) <= idx {
						choice.Message.ToolCalls = append(choice.Message.ToolCalls, api.ToolCall{
							Type: "function",
						})
					}
					// Merge delta into tool call
					if tc.ID != "" {
						choice.Message.ToolCalls[idx].ID = tc.ID
					}
					if tc.Function.Name != "" {
						choice.Message.ToolCalls[idx].Function.Name = tc.Function.Name
					}
					choice.Message.ToolCalls[idx].Function.Arguments += tc.Function.Arguments
				}
			}
		}
		if cc.FinishReason != nil {
			choice.FinishReason = cc.FinishReason
		}
	}

	// Capture usage
	if chunk.Usage != nil {
		s.response.Usage = chunk.Usage
	}
}

// Response returns the accumulated non-streaming response.
func (s *Stream) Response() *api.ChatCompletionResponse {
	return s.response
}

// Err returns any error that occurred during streaming.
func (s *Stream) Err() error {
	return s.err
}

// Close releases resources associated with the stream.
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
	}

	if err := json.Unmarshal(body, &errResp); err == nil {
		if errResp.Error.Message != "" {
			return errResp.Error.Message
		}
		if errResp.Message != "" {
			return errResp.Message
		}
	}

	bodyStr := string(body)
	if len(bodyStr) > 500 {
		bodyStr = bodyStr[:500] + "..."
	}
	if bodyStr == "" {
		return "unknown error"
	}
	return bodyStr
}

// NonStreamingRead reads the entire response for non-streaming mode.
func (s *Stream) NonStreamingRead() (*api.ChatCompletionResponse, error) {
	for {
		_, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.response, nil
			}
			return nil, err
		}
	}
}
