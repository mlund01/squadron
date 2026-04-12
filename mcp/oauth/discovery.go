package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// discoverAuthServerMetadataURL resolves the authorization server metadata
// URL for an MCP server by following RFC 9728 (OAuth Protected Resource
// Metadata). The .well-known endpoint is at the origin level per RFC 8615,
// NOT at the MCP path — e.g. for https://fellow.app/mcp the protected
// resource metadata lives at https://fellow.app/.well-known/oauth-protected-resource.
//
// Discovery chain:
//  1. GET <origin>/.well-known/oauth-protected-resource → { authorization_servers: [...] }
//  2. Return <auth_server>/.well-known/oauth-authorization-server
//
// Falls back to <origin>/.well-known/oauth-authorization-server if the
// protected resource endpoint doesn't exist.
func DiscoverAuthServerMetadataURL(ctx context.Context, serverURL string) (string, error) {
	origin, err := extractOrigin(serverURL)
	if err != nil {
		return "", err
	}

	// Try RFC 9728 protected resource discovery first.
	prURL := origin + "/.well-known/oauth-protected-resource"
	authServer, err := fetchProtectedResourceAuthServer(ctx, prURL)
	if err == nil && authServer != "" {
		return authServer + "/.well-known/oauth-authorization-server", nil
	}

	// Fall back to direct auth server discovery at the origin.
	return origin + "/.well-known/oauth-authorization-server", nil
}

type protectedResourceResponse struct {
	AuthorizationServers []string `json:"authorization_servers"`
}

func fetchProtectedResourceAuthServer(ctx context.Context, prURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, prURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var pr protectedResourceResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", err
	}
	if len(pr.AuthorizationServers) == 0 {
		return "", fmt.Errorf("no authorization_servers in response")
	}
	return pr.AuthorizationServers[0], nil
}

func extractOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL: %s", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}
