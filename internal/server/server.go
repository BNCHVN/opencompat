package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/edgard/opencompat/internal/api"
	"github.com/edgard/opencompat/internal/config"
	"github.com/edgard/opencompat/internal/provider"
)

// Server represents the HTTP server.
type Server struct {
	httpServer *http.Server
	handlers   *Handlers
	registry   *provider.Registry
	cfg        *config.Config
}

// New creates a new server instance.
func New(registry *provider.Registry, cfg *config.Config) *Server {
	handlers := NewHandlers(registry, cfg)

	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("/health", handlers.Health)
	mux.HandleFunc("/v1/models", handlers.Models)
	mux.HandleFunc("/v1/chat/completions", handlers.ChatCompletions)

	// Catch-all for unknown /v1/ endpoints - returns OpenAI-style 404
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		// Check if this path matches a known endpoint (exact match handled above)
		path := r.URL.Path
		if path == "/v1/models" || path == "/v1/chat/completions" {
			// Shouldn't reach here due to exact match, but just in case
			return
		}
		// Return OpenAI-style 404 for unknown /v1/ paths
		endpoint := strings.TrimPrefix(path, "/v1/")
		api.WriteNotFound(w, fmt.Sprintf("Unknown endpoint: /v1/%s", endpoint))
	})

	// Apply middleware
	handler := ChainMiddleware(
		mux,
		RecoveryMiddleware,
		LoggingMiddleware,
		RequestIDMiddleware,
		CORSMiddleware,
	)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	return &Server{
		httpServer: &http.Server{
			Addr:    addr,
			Handler: handler,
		},
		handlers: handlers,
		registry: registry,
		cfg:      cfg,
	}
}

// PrefetchInstructions initializes all active providers.
// This should be called before Start().
func (s *Server) PrefetchInstructions() error {
	// Initialize all lifecycle providers
	for _, meta := range s.registry.ListMetas() {
		p, ok := s.registry.GetActiveProvider(meta.ID)
		if !ok {
			continue
		}
		if lp, ok := p.(provider.LifecycleProvider); ok {
			if err := lp.Init(); err != nil {
				return fmt.Errorf("failed to initialize provider %s: %w", meta.ID, err)
			}
		}
	}
	return nil
}

// Start starts the HTTP server.
// Should be called after PrefetchInstructions().
func (s *Server) Start() error {
	// Start all lifecycle providers
	for _, meta := range s.registry.ListMetas() {
		p, ok := s.registry.GetActiveProvider(meta.ID)
		if !ok {
			continue
		}
		if lp, ok := p.(provider.LifecycleProvider); ok {
			lp.Start()
		}
	}

	slog.Info("server starting", "addr", s.httpServer.Addr)
	slog.Info("OpenAI-compatible API available", "url", fmt.Sprintf("http://%s/v1", s.httpServer.Addr))

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// Close all providers
	s.registry.CloseAll()

	return s.httpServer.Shutdown(ctx)
}
