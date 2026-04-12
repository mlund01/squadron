package oauth

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

// CallbackParams carries the code/state pair returned from the authorization
// server.
type CallbackParams struct {
	Code  string
	State string
}

// CallbackSource abstracts how the flow gets the auth code back from the
// user's browser. LoopbackCallbackSource serves the callback locally for the
// CLI; a wsbridge-backed source will route through command center in Phase 2.
type CallbackSource interface {
	Prepare(ctx context.Context) (redirectURI string, err error)
	Present(ctx context.Context, authURL string) error
	Wait(ctx context.Context) (CallbackParams, error)
	Close() error
}

// RunLoginFlow runs discovery, DCR (if needed), PKCE, browser consent, and
// token exchange for one MCP server. On success the token is already in the
// vault via the handler's VaultTokenStore.
func RunLoginFlow(ctx context.Context, name, serverURL string, source CallbackSource) error {
	if name == "" || serverURL == "" {
		return fmt.Errorf("oauth: login requires name and server URL")
	}

	redirectURI, err := source.Prepare(ctx)
	if err != nil {
		return fmt.Errorf("oauth %q: preparing callback: %w", name, err)
	}
	defer source.Close()

	creds, err := LoadClientCredentials(name)
	if err != nil {
		return err
	}

	// Do our own RFC 9728 discovery to find the auth server metadata URL.
	// mcp-go's buildWellKnownURL appends the MCP path as a suffix (e.g.
	// /.well-known/oauth-protected-resource/mcp) which breaks real servers
	// that serve .well-known at the origin level per RFC 8615.
	authMetadataURL, err := DiscoverAuthServerMetadataURL(ctx, serverURL)
	if err != nil {
		return fmt.Errorf("oauth %q: this server does not appear to support OAuth.\n"+
			"  Could not discover an authorization server at the .well-known endpoint.\n"+
			"  Refer to the MCP server's documentation for how to authorize.", name)
	}

	cfg := transport.OAuthConfig{
		RedirectURI:           redirectURI,
		TokenStore:            NewVaultTokenStore(name),
		PKCEEnabled:           true,
		AuthServerMetadataURL: authMetadataURL,
	}
	if creds != nil {
		cfg.ClientID = creds.ClientID
		cfg.ClientSecret = creds.ClientSecret
	}

	handler := transport.NewOAuthHandler(cfg)
	handler.SetBaseURL(serverURL)

	if _, err := handler.GetServerMetadata(ctx); err != nil {
		return fmt.Errorf("oauth %q: failed to load authorization server metadata: %w", name, err)
	}

	if handler.GetClientID() == "" {
		if err := handler.RegisterClient(ctx, "squadron"); err != nil {
			// DCR is optional — many servers accept authorization requests
			// without a registered client_id, relying on PKCE + redirect_uri
			// for client identity. Continue without it.
			fmt.Printf("  Note: dynamic client registration not supported, continuing without it.\n")
		} else {
			if err := SaveClientCredentials(name, ClientCredentials{
				ClientID:     handler.GetClientID(),
				ClientSecret: handler.GetClientSecret(),
			}); err != nil {
				return fmt.Errorf("oauth %q: caching client credentials: %w", name, err)
			}
		}
	}

	verifier, err := transport.GenerateCodeVerifier()
	if err != nil {
		return fmt.Errorf("oauth %q: generating PKCE verifier: %w", name, err)
	}
	challenge := transport.GenerateCodeChallenge(verifier)

	state, err := transport.GenerateState()
	if err != nil {
		return fmt.Errorf("oauth %q: generating state: %w", name, err)
	}
	handler.SetExpectedState(state)

	authURL, err := handler.GetAuthorizationURL(ctx, state, challenge)
	if err != nil {
		return fmt.Errorf("oauth %q: building authorization URL: %w", name, err)
	}

	if err := source.Present(ctx, authURL); err != nil {
		return fmt.Errorf("oauth %q: presenting authorization URL: %w", name, err)
	}

	// Bound the wait so an abandoned browser doesn't hang the CLI forever.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	params, err := source.Wait(waitCtx)
	if err != nil {
		return fmt.Errorf("oauth %q: waiting for callback: %w", name, err)
	}

	if err := handler.ProcessAuthorizationResponse(ctx, params.Code, params.State, verifier); err != nil {
		return fmt.Errorf("oauth %q: exchanging authorization code: %w", name, err)
	}
	return nil
}
