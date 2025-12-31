package api

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents an OpenAI API error response.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the error information.
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// Common error types
const (
	ErrorTypeInvalidRequest     = "invalid_request_error"
	ErrorTypeAuthentication     = "authentication_error"
	ErrorTypeNotFound           = "not_found_error"
	ErrorTypeRateLimit          = "rate_limit_error"
	ErrorTypeServer             = "server_error"
	ErrorTypeServiceUnavailable = "service_unavailable"
)

// WriteError writes an OpenAI-compatible error response.
func WriteError(w http.ResponseWriter, statusCode int, errType, message string, code, param *string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	resp := ErrorResponse{
		Error: ErrorDetail{
			Message: message,
			Type:    errType,
			Code:    code,
			Param:   param,
		},
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// WriteBadRequest writes a 400 error.
func WriteBadRequest(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, ErrorTypeInvalidRequest, message, nil, nil)
}

// WriteBadRequestWithParam writes a 400 error with a specific parameter.
func WriteBadRequestWithParam(w http.ResponseWriter, message, param string) {
	WriteError(w, http.StatusBadRequest, ErrorTypeInvalidRequest, message, nil, &param)
}

// WriteNotFound writes a 404 error.
func WriteNotFound(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusNotFound, ErrorTypeNotFound, message, nil, nil)
}

// WriteMethodNotAllowed writes a 405 error.
func WriteMethodNotAllowed(w http.ResponseWriter) {
	WriteError(w, http.StatusMethodNotAllowed, ErrorTypeInvalidRequest, "Method not allowed", nil, nil)
}

// WriteServerError writes a 500 error.
func WriteServerError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusInternalServerError, ErrorTypeServer, message, nil, nil)
}

// WriteModelNotFound writes a model not found error.
func WriteModelNotFound(w http.ResponseWriter, model string) {
	code := "model_not_found"
	message := "The model `" + model + "` does not exist or you do not have access to it."
	WriteError(w, http.StatusNotFound, ErrorTypeNotFound, message, &code, nil)
}

// UpstreamError represents an error from an upstream provider with HTTP status.
type UpstreamError struct {
	StatusCode int
	Message    string
}

// Error implements the error interface.
func (e *UpstreamError) Error() string {
	return e.Message
}

// NewUpstreamError creates a new UpstreamError.
func NewUpstreamError(statusCode int, message string) *UpstreamError {
	return &UpstreamError{
		StatusCode: statusCode,
		Message:    message,
	}
}

// WriteUpstreamError writes an error response based on upstream status code.
// Maps upstream HTTP status codes to appropriate OpenAI-compatible error responses.
func WriteUpstreamError(w http.ResponseWriter, err *UpstreamError) {
	if err == nil {
		WriteServerError(w, "Unknown upstream error")
		return
	}

	switch err.StatusCode {
	case http.StatusBadRequest:
		WriteError(w, http.StatusBadRequest, ErrorTypeInvalidRequest, err.Message, nil, nil)
	case http.StatusUnauthorized:
		WriteError(w, http.StatusUnauthorized, ErrorTypeAuthentication, err.Message, nil, nil)
	case http.StatusForbidden:
		WriteError(w, http.StatusForbidden, ErrorTypeAuthentication, err.Message, nil, nil)
	case http.StatusNotFound:
		WriteError(w, http.StatusNotFound, ErrorTypeNotFound, err.Message, nil, nil)
	case http.StatusTooManyRequests:
		WriteError(w, http.StatusTooManyRequests, ErrorTypeRateLimit, err.Message, nil, nil)
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		WriteError(w, err.StatusCode, ErrorTypeServiceUnavailable, err.Message, nil, nil)
	default:
		// For other errors, use 502 Bad Gateway to indicate upstream failure
		WriteError(w, http.StatusBadGateway, ErrorTypeServer, err.Message, nil, nil)
	}
}
