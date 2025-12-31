package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/edgard/opencompat/internal/api"
)

// SSEWriter helps write SSE events to the client.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates a new SSE writer.
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	return &SSEWriter{w: w, flusher: flusher}, nil
}

// WriteChunk writes a chat completion chunk as an SSE event.
func (s *SSEWriter) WriteChunk(chunk *api.ChatCompletionChunk) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(s.w, "data: %s\n\n", data)
	if err != nil {
		return err
	}

	s.flusher.Flush()
	return nil
}

// WriteDone writes the [DONE] marker.
func (s *SSEWriter) WriteDone() error {
	_, err := fmt.Fprint(s.w, "data: [DONE]\n\n")
	if err != nil {
		return err
	}

	s.flusher.Flush()
	return nil
}

// WriteError writes an error as an SSE event.
func (s *SSEWriter) WriteError(message string) error {
	errResp := api.ErrorResponse{
		Error: api.ErrorDetail{
			Message: message,
			Type:    api.ErrorTypeServer,
		},
	}

	data, err := json.Marshal(errResp)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(s.w, "data: %s\n\n", data)
	if err != nil {
		return err
	}

	s.flusher.Flush()
	return nil
}
