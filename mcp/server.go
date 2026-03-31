package mcp

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"squadron/config"
	"squadron/store"
)

// Deps holds all dependencies needed by the MCP server.
type Deps struct {
	// Config returns the current config snapshot (may be hot-reloaded).
	Config func() *config.Config
	// Stores provides access to mission/task/event/session data.
	Stores *store.Bundle
	// Version is the squadron CLI version string.
	Version string
	// ConfigPath is the path to the config directory.
	ConfigPath string
	// RunMission kicks off a mission by name with optional inputs, returning the mission ID.
	RunMission func(name string, inputs map[string]string) (string, error)
	// ReloadConfig re-reads and validates config from disk, swapping it in if valid.
	ReloadConfig func() error
}

// Server wraps the MCP server and SSE transport.
type Server struct {
	sse      *server.SSEServer
	httpSrv  *http.Server // only set when using auth wrapper
}

// NewServer creates a configured MCP server with all squadron tools registered.
func NewServer(deps Deps) *server.MCPServer {
	srv := server.NewMCPServer(
		"squadron",
		deps.Version,
		server.WithToolCapabilities(true),
	)

	h := &handlers{deps: deps}
	registerTools(srv, h)

	return srv
}

// StartSSE starts an SSE-based MCP server on the given port.
// If secret is non-empty, requests must provide it as a Bearer token or query param.
// Returns a Server handle (for shutdown) and any startup error.
func StartSSE(srv *server.MCPServer, port int, secret string) (*Server, error) {
	addr := fmt.Sprintf(":%d", port)

	sseServer := server.NewSSEServer(srv)

	if secret != "" {
		// When using auth, we start our own HTTP server that wraps the SSE handler
		httpSrv := &http.Server{
			Addr:    addr,
			Handler: authMiddleware(secret, sseServer),
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- httpSrv.ListenAndServe()
		}()

		select {
		case err := <-errCh:
			return nil, fmt.Errorf("mcp server failed to start on %s: %w", addr, err)
		default:
			return &Server{sse: sseServer, httpSrv: httpSrv}, nil
		}
	}

	// No auth — let the SSE server manage its own HTTP server
	errCh := make(chan error, 1)
	go func() {
		errCh <- sseServer.Start(addr)
	}()

	select {
	case err := <-errCh:
		return nil, fmt.Errorf("mcp server failed to start on %s: %w", addr, err)
	default:
		return &Server{sse: sseServer}, nil
	}
}

// Shutdown gracefully shuts down the MCP server.
func (s *Server) Shutdown() error {
	if s == nil {
		return nil
	}
	if s.httpSrv != nil {
		return s.httpSrv.Shutdown(context.Background())
	}
	if s.sse != nil {
		return s.sse.Shutdown(context.Background())
	}
	return nil
}

// authMiddleware wraps an HTTP handler with Bearer token / query param authentication.
func authMiddleware(secret string, next http.Handler) http.Handler {
	secretBytes := []byte(secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""

		// Check Authorization header first
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}

		// Fall back to query param
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		if token == "" || subtle.ConstantTimeCompare([]byte(token), secretBytes) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// toolResult is a convenience for creating JSON tool results.
func toolResult(data any) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultJSON(data)
}
