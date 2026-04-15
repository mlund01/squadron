package oauth

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
)

// RefreshCallback is called after a token is successfully refreshed. The
// MCP client uses this to reconnect SSE streams that hold a stale bearer
// token from Start().
type RefreshCallback func()

// StartRefreshLoop spawns a goroutine that refreshes the token for the
// named MCP server before it expires. It uses the vault-backed store to
// read the current token, refreshes it via the OAuthHandler, and writes
// the new token back. If a callback is set, it's invoked after each
// successful refresh so the caller can reconnect stale transports.
//
// The loop exits when ctx is cancelled (typically via Client.Close).
func StartRefreshLoop(ctx context.Context, name, serverURL string, onRefresh RefreshCallback) {
	go refreshLoop(ctx, name, serverURL, onRefresh)
}

func refreshLoop(ctx context.Context, name, serverURL string, onRefresh RefreshCallback) {
	store := NewVaultTokenStore(name)

	for {
		tok, err := store.GetToken(ctx)
		if err != nil || tok == nil || tok.RefreshToken == "" {
			// No token or no refresh token — nothing to do. Sleep and
			// recheck in case a login happens while we're running.
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}

		// Refresh 2 minutes before expiry, or immediately if already expired.
		refreshAt := tok.ExpiresAt.Add(-10 * time.Minute)
		waitDur := time.Until(refreshAt)
		if waitDur <= 0 {
			waitDur = 0
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(waitDur):
		}

		if err := doRefresh(ctx, name, serverURL, store, tok); err != nil {
			log.Printf("mcp %q: token refresh failed: %v", name, err)
			// Back off briefly and retry on next loop iteration.
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}

		if onRefresh != nil {
			onRefresh()
		}
	}
}

// ForceRefresh reads the stored token and immediately refreshes it.
func ForceRefresh(ctx context.Context, name, serverURL string) error {
	store := NewVaultTokenStore(name)
	tok, err := store.GetToken(ctx)
	if err != nil {
		return fmt.Errorf("oauth %q: %w", name, err)
	}
	if tok.RefreshToken == "" {
		return fmt.Errorf("oauth %q: stored token has no refresh token", name)
	}
	return doRefresh(ctx, name, serverURL, store, tok)
}

func doRefresh(ctx context.Context, name, serverURL string, store *VaultTokenStore, tok *transport.Token) error {
	creds, _ := LoadClientCredentials(name)

	cfg := transport.OAuthConfig{
		TokenStore:  store,
		PKCEEnabled: true,
	}
	if creds != nil {
		cfg.ClientID = creds.ClientID
		cfg.ClientSecret = creds.ClientSecret
	}
	if metaURL, err := DiscoverAuthServerMetadataURL(ctx, serverURL); err == nil {
		cfg.AuthServerMetadataURL = metaURL
	}

	handler := transport.NewOAuthHandler(cfg)
	handler.SetBaseURL(serverURL)

	if _, err := handler.GetServerMetadata(ctx); err != nil {
		return err
	}

	newTok, err := handler.RefreshToken(ctx, tok.RefreshToken)
	if err != nil {
		return err
	}

	return store.SaveToken(ctx, newTok)
}
