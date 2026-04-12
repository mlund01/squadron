package mcp

import (
	"errors"
	"fmt"

	mcpgoclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

// classifyAuthError returns an AuthRequiredError for any error shape that
// means "user needs to run squadron mcp login"; otherwise returns nil so the
// caller can fall back to normal error wrapping.
func classifyAuthError(name, url string, err error) error {
	if err == nil {
		return nil
	}
	if mcpgoclient.IsOAuthAuthorizationRequiredError(err) ||
		errors.Is(err, transport.ErrOAuthAuthorizationRequired) ||
		errors.Is(err, transport.ErrUnauthorized) {
		return &AuthRequiredError{Name: name, URL: url, Cause: err}
	}
	return nil
}

// AuthRequiredError is returned from mcp.Load when an HTTP MCP needs OAuth
// and no token is stored. Its Error() is user-facing with a concrete fix.
type AuthRequiredError struct {
	Name  string
	URL   string
	Cause error
}

func (e *AuthRequiredError) Error() string {
	return fmt.Sprintf(
		"mcp %q: authorization required\n"+
			"  This server uses OAuth. Run:\n"+
			"    squadron mcp login %s",
		e.Name, e.Name,
	)
}

func (e *AuthRequiredError) Unwrap() error { return e.Cause }
