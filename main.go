// OpenCompat provides a personal API compatibility layer.
//
// This is an independent open-source project for personal, non-commercial use.
// It is NOT affiliated with, endorsed by, or sponsored by OpenAI or any other company.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/edgard/opencompat/internal/auth"
	"github.com/edgard/opencompat/internal/config"
	"github.com/edgard/opencompat/internal/logging"
	"github.com/edgard/opencompat/internal/provider"
	_ "github.com/edgard/opencompat/internal/provider/chatgpt" // Register chatgpt provider
	_ "github.com/edgard/opencompat/internal/provider/copilot" // Register copilot provider
	"github.com/edgard/opencompat/internal/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usageHeader = `OpenCompat - Personal API compatibility layer

Usage:
  opencompat [command]

Commands:
  login <provider>    Authenticate with a provider (e.g., chatgpt)
  logout <provider>   Remove credentials for a provider
  info                Show authentication status for all providers
  models              List all supported providers and models
  serve               Start the API server (default)
  version             Show version information
  help                Show this help message
`

// buildUsage constructs the full usage string with dynamic provider information.
func buildUsage() string {
	var sb strings.Builder
	sb.WriteString(usageHeader)

	registry := provider.NewRegistry()
	provider.RegisterAll(registry)
	metas := registry.ListMetas()

	// Providers section
	sb.WriteString("\nProviders:\n")
	for _, meta := range metas {
		authDesc := "unknown"
		switch meta.AuthMethod {
		case auth.AuthMethodOAuth:
			authDesc = "OAuth browser login"
		case auth.AuthMethodAPIKey:
			authDesc = "API key"
		case auth.AuthMethodDeviceFlow:
			authDesc = "GitHub device login"
		}
		sb.WriteString(fmt.Sprintf("  %-19s %s (%s)\n", meta.ID, meta.Name, authDesc))
	}

	// Global environment variables
	sb.WriteString("\nEnvironment Variables (Global):\n")
	sb.WriteString(fmt.Sprintf("  %-44s %s (default: %s)\n", "OPENCOMPAT_HOST", "Server bind address", "127.0.0.1"))
	sb.WriteString(fmt.Sprintf("  %-44s %s (default: %s)\n", "OPENCOMPAT_PORT", "Server listen port", "8080"))
	sb.WriteString(fmt.Sprintf("  %-44s %s (default: %s)\n", "OPENCOMPAT_LOG_LEVEL", "Log level (debug, info, warn, error)", "info"))
	sb.WriteString(fmt.Sprintf("  %-44s %s (default: %s)\n", "OPENCOMPAT_LOG_FORMAT", "Log format (text, json)", "text"))

	// Provider-specific environment variables
	for _, meta := range metas {
		if len(meta.EnvVars) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\nEnvironment Variables (%s):\n", meta.Name))
		for _, env := range meta.EnvVars {
			sb.WriteString(fmt.Sprintf("  %-44s %s (default: %s)\n", env.Name, env.Description, env.Default))
		}
	}

	return sb.String()
}

const acknowledgmentFile = "acknowledged"

const disclaimerText = `
================================================================================
                                    NOTICE
================================================================================

This is an independent open-source project for PERSONAL, NON-COMMERCIAL USE.
It is NOT affiliated with, endorsed by, or sponsored by OpenAI or any other
company.

By using this software, you acknowledge that:

1. You are responsible for compliance with all applicable terms of service.

2. You assume all risk for any consequences of your use.

3. The author is not liable for any damages arising from your use.

4. This software is provided "AS IS" without warranty of any kind.

For full terms, see the LICENSE file.

================================================================================
`

// getProviderIDs returns the list of registered provider IDs.
func getProviderIDs() []string {
	registry := provider.NewRegistry()
	provider.RegisterAll(registry)
	metas := registry.ListMetas()
	ids := make([]string, len(metas))
	for i, m := range metas {
		ids[i] = m.ID
	}
	return ids
}

func main() {
	// Initialize logging for all commands
	cfg := config.Load()
	logging.Setup(cfg.LogLevel, cfg.LogFormat)

	if len(os.Args) < 2 {
		cmdServe()
		return
	}

	switch os.Args[1] {
	case "login":
		cmdLogin()
	case "logout":
		cmdLogout()
	case "info":
		cmdInfo()
	case "models":
		cmdModels()
	case "serve":
		cmdServe()
	case "version", "-v", "--version":
		fmt.Printf("opencompat %s (commit: %s, built: %s)\n", version, commit, date)
	case "help", "-h", "--help":
		fmt.Print(buildUsage())
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		fmt.Print(buildUsage())
		os.Exit(1)
	}
}

func cmdLogin() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Error: provider argument required")
		fmt.Fprintln(os.Stderr, "Usage: opencompat login <provider>")
		fmt.Fprintln(os.Stderr, "\nAvailable providers:")
		for _, p := range getProviderIDs() {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		os.Exit(1)
	}

	providerID := strings.ToLower(os.Args[2])
	store := auth.NewStore()
	registry := provider.NewRegistry()
	provider.RegisterAll(registry)

	meta, ok := registry.GetMeta(providerID)
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown provider: %s\n", providerID)
		fmt.Fprintln(os.Stderr, "\nAvailable providers:")
		for _, p := range getProviderIDs() {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		os.Exit(1)
	}

	// Perform login based on auth method
	switch meta.AuthMethod {
	case auth.AuthMethodOAuth:
		if err := auth.PerformOAuthLogin(store, providerID, meta.OAuthCfg); err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}
	case auth.AuthMethodDeviceFlow:
		if err := auth.PerformDeviceFlowLogin(store, providerID, meta.DeviceFlowCfg); err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}
	case auth.AuthMethodAPIKey:
		fmt.Print("Enter API key: ")
		apiKeyBytes, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // Print newline after hidden input
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read API key: %v\n", err)
			os.Exit(1)
		}
		apiKey := strings.TrimSpace(string(apiKeyBytes))
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "API key cannot be empty")
			os.Exit(1)
		}
		creds := &auth.APIKeyCredentials{
			APIKey:    apiKey,
			CreatedAt: time.Now(),
		}
		if err := store.SaveAPIKeyCredentials(providerID, creds); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Logged in to %s successfully.\n", providerID)
	default:
		fmt.Fprintf(os.Stderr, "Unsupported auth method for provider: %s\n", providerID)
		os.Exit(1)
	}
}

func cmdLogout() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Error: provider argument required")
		fmt.Fprintln(os.Stderr, "Usage: opencompat logout <provider>")
		fmt.Fprintln(os.Stderr, "\nAvailable providers:")
		for _, p := range getProviderIDs() {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		os.Exit(1)
	}

	providerID := strings.ToLower(os.Args[2])
	store := auth.NewStore()
	registry := provider.NewRegistry()
	provider.RegisterAll(registry)

	// Check if it's a known provider
	if _, ok := registry.GetMeta(providerID); !ok {
		fmt.Fprintf(os.Stderr, "Unknown provider: %s\n", providerID)
		os.Exit(1)
	}

	if err := store.DeleteCredentials(providerID); err != nil {
		fmt.Fprintf(os.Stderr, "Logout failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Logged out of %s successfully.\n", providerID)
}

func cmdInfo() {
	store := auth.NewStore()
	registry := provider.NewRegistry()
	provider.RegisterAll(registry)

	fmt.Println("Provider Status:")
	fmt.Println()

	for _, meta := range registry.ListMetas() {
		fmt.Printf("  %s (%s):\n", meta.Name, meta.ID)

		if !store.IsLoggedIn(meta.ID) {
			fmt.Printf("    Status: Not logged in\n")
			fmt.Printf("    Login:  opencompat login %s\n", meta.ID)
			fmt.Println()
			continue
		}

		switch meta.AuthMethod {
		case auth.AuthMethodOAuth:
			creds, err := store.GetOAuthCredentials(meta.ID)
			if err != nil {
				fmt.Printf("    Status: Error loading credentials\n")
				fmt.Println()
				continue
			}
			fmt.Printf("    Status: Logged in\n")
			if creds.Email != "" {
				fmt.Printf("    Email:  %s\n", creds.Email)
			}
			if creds.AccountID != "" {
				fmt.Printf("    Account: %s\n", creds.AccountID)
			}
			fmt.Printf("    Expires: %s\n", creds.ExpiresAt.Format("2006-01-02 15:04:05"))
			if creds.IsExpired() {
				fmt.Printf("    Token:  Expired (will refresh on next request)\n")
			} else {
				fmt.Printf("    Token:  Valid\n")
			}

		case auth.AuthMethodAPIKey:
			creds, err := store.GetAPIKeyCredentials(meta.ID)
			if err != nil {
				fmt.Printf("    Status: Error loading credentials\n")
				fmt.Println()
				continue
			}
			fmt.Printf("    Status: Logged in\n")
			// Show masked API key
			if len(creds.APIKey) > 8 {
				fmt.Printf("    API Key: %s...%s\n", creds.APIKey[:4], creds.APIKey[len(creds.APIKey)-4:])
			} else {
				fmt.Printf("    API Key: ****\n")
			}
			fmt.Printf("    Created: %s\n", creds.CreatedAt.Format("2006-01-02 15:04:05"))

		case auth.AuthMethodDeviceFlow:
			// Device flow uses OAuth credentials (refresh token is the GitHub token)
			creds, err := store.GetOAuthCredentials(meta.ID)
			if err != nil {
				fmt.Printf("    Status: Error loading credentials\n")
				fmt.Println()
				continue
			}
			fmt.Printf("    Status: Logged in\n")
			// Show masked GitHub token
			if len(creds.RefreshToken) > 8 {
				fmt.Printf("    Token: %s...%s\n", creds.RefreshToken[:4], creds.RefreshToken[len(creds.RefreshToken)-4:])
			} else {
				fmt.Printf("    Token: ****\n")
			}
		}
		fmt.Println()
	}
}

func cmdModels() {
	store := auth.NewStore()
	registry := provider.NewRegistry()
	provider.RegisterAll(registry)

	fmt.Println("Refreshing models from providers...")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, meta := range registry.ListMetas() {
		// Get provider instance to list models
		// Pass store so providers can fetch dynamic models if logged in
		p, err := meta.Factory(store)
		if err != nil {
			fmt.Printf("  %s (%s): error loading provider\n", meta.Name, meta.ID)
			continue
		}

		// Force refresh if provider supports it
		if refresher, ok := p.(provider.Refresher); ok {
			if err := refresher.RefreshModels(ctx); err != nil {
				fmt.Printf("  %s (%s): refresh failed: %v\n", meta.Name, meta.ID, err)
				// Continue to show cached models anyway
			}
		}

		models := p.Models()
		fmt.Printf("  %s (%s):\n", meta.Name, meta.ID)

		if len(models) == 0 {
			fmt.Printf("    (no models available - login required?)\n")
		} else {
			for _, m := range models {
				// Show model with provider prefix as used in API
				fmt.Printf("    %s/%s\n", meta.ID, m.ID)
			}
		}
		fmt.Println()
	}

	fmt.Println("Note: ChatGPT models support effort suffixes: -low, -medium, -high")
	fmt.Println("Example: chatgpt/gpt-5.1-codex-high")
}

func cmdServe() {
	// Check acknowledgment first
	if err := checkAcknowledgment(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	cfg := config.Load()
	store := auth.NewStore()
	registry := provider.NewRegistry()
	provider.RegisterAll(registry)

	// Initialize providers (only those logged in will activate)
	if err := registry.Initialize(store); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize providers: %v\n", err)
		os.Exit(1)
	}

	// Check if at least one provider is active
	if !registry.HasProviders() {
		fmt.Fprintln(os.Stderr, "No providers available. Please log in to at least one provider:")
		for _, meta := range registry.ListMetas() {
			fmt.Fprintf(os.Stderr, "  opencompat login %s\n", meta.ID)
		}
		os.Exit(1)
	}

	srv := server.New(registry, cfg)

	// Prefetch instructions before starting server
	// This ensures all model instructions are available
	if err := srv.PrefetchInstructions(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to prefetch instructions: %v\n", err)
		fmt.Fprintln(os.Stderr, "Server cannot start without instructions.")
		os.Exit(1)
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start()
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		slog.Info("received signal, shutting down", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
		slog.Info("server stopped")
	case err := <-errChan:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}
}

func checkAcknowledgment() error {
	ackPath := filepath.Join(config.DataDir(), acknowledgmentFile)

	// Check if already acknowledged
	if _, err := os.Stat(ackPath); err == nil {
		return nil
	}

	// Display disclaimer
	fmt.Print(disclaimerText)
	fmt.Print("\nDo you understand and agree to these terms? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response != "yes" {
		return fmt.Errorf("you must agree to the terms to use this software")
	}

	// Save acknowledgment
	if err := config.EnsureDataDir(); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	ackContent := fmt.Sprintf("Acknowledged: %s\n", time.Now().Format(time.RFC3339))
	if err := os.WriteFile(ackPath, []byte(ackContent), 0600); err != nil {
		return fmt.Errorf("failed to save acknowledgment: %w", err)
	}

	fmt.Println("\nThank you. Starting server...")
	return nil
}
