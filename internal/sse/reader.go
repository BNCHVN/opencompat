// Package sse provides Server-Sent Events parsing.
package sse

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Event represents a parsed SSE event.
type Event struct {
	Event string
	Data  json.RawMessage
	ID    string
	Retry int
}

// Reader reads SSE events from an HTTP response.
type Reader struct {
	reader *bufio.Reader
	done   bool
}

// NewReader creates a new SSE reader.
func NewReader(r io.Reader) *Reader {
	return &Reader{
		reader: bufio.NewReader(r),
	}
}

// ReadEvent reads the next SSE event.
func (r *Reader) ReadEvent() (*Event, error) {
	if r.done {
		return nil, io.EOF
	}

	var event Event
	var dataLines []string

	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				r.done = true
				if len(dataLines) > 0 {
					// Process any remaining data
					break
				}
			}
			return nil, err
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		// Empty line signals end of event
		if line == "" {
			if event.Event != "" || len(dataLines) > 0 {
				break
			}
			continue
		}

		// Parse field
		if strings.HasPrefix(line, "event:") {
			event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				r.done = true
				return nil, io.EOF
			}
			dataLines = append(dataLines, data)
		} else if strings.HasPrefix(line, "id:") {
			event.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		} else if strings.HasPrefix(line, "retry:") {
			if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "retry:"))); err == nil {
				event.Retry = v
			}
		}
		// Ignore comments (lines starting with :)
	}

	// Combine data lines
	if len(dataLines) > 0 {
		event.Data = json.RawMessage(strings.Join(dataLines, "\n"))
	}

	return &event, nil
}
