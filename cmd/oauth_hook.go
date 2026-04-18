package cmd

import (
	"context"
	"fmt"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/config"
	"squadron/mcp/oauth"
	"squadron/wsbridge"
)

// installOAuthProxyHook wires wsbridge.StartMCPLoginHook so commander-initiated
// MCP login requests can kick off the OAuth flow inside this running engage
// process, using the WsbridgeCallbackSource to route the callback back
// through command center.
//
// The hook returns the authorization URL synchronously; the rest of the
// login (code exchange + token persistence) runs in the background. The
// browser that started the flow is expected to open the URL in a new tab.
func installOAuthProxyHook() {
	wsbridge.StartMCPLoginHook = runWSBridgeLogin
}

// wsbridgeClientAdapter bridges wsbridge.Client to oauth.WSCaller by
// translating wsbridge.OAuthCallback ↔ oauth.WSCallback.
type wsbridgeClientAdapter struct{ c *wsbridge.Client }

func (a wsbridgeClientAdapter) SendRequest(env *protocol.Envelope) (*protocol.Envelope, error) {
	return a.c.SendRequest(env)
}

func (a wsbridgeClientAdapter) RegisterOAuthListener(state string, ch chan<- oauth.WSCallback) func() {
	// We bridge the channels: wsbridge delivers wsbridge.OAuthCallback, but
	// oauth expects oauth.WSCallback (identical shape, different package).
	relay := make(chan wsbridge.OAuthCallback, 1)
	cancel := a.c.RegisterOAuthListener(state, relay)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case cb, ok := <-relay:
			if !ok {
				return
			}
			ch <- oauth.WSCallback{Code: cb.Code, State: cb.State, Error: cb.Error}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func runWSBridgeLogin(ctx context.Context, client *wsbridge.Client, mcpName string) (string, error) {
	cfg := client.GetConfig()
	if cfg == nil || cfg.CommandCenter == nil {
		return "", fmt.Errorf("command_center not configured")
	}
	if cfg.CommandCenter.Host == "" {
		return "", fmt.Errorf("command_center.host is empty")
	}

	// Look up the MCP spec by name from the currently loaded config.
	var spec *config.MCPServer
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == mcpName {
			spec = &cfg.MCPServers[i]
			break
		}
	}
	if spec == nil {
		return "", fmt.Errorf("mcp %q: not found in config", mcpName)
	}
	if spec.URL == "" {
		return "", fmt.Errorf("mcp %q: OAuth only applies to HTTP (url) servers", mcpName)
	}

	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)

	source := oauth.NewWsbridgeCallbackSource(
		wsbridgeClientAdapter{c: client},
		mcpName,
		cfg.CommandCenter.OAuthRedirectURI(),
	)
	// Instead of opening a browser inside this process, ferry the auth URL
	// back to the waiting request so commander can return it to the browser
	// that initiated the login.
	source.AuthURLHook = func(authURL string) {
		select {
		case urlCh <- authURL:
		default:
		}
	}

	// Run the login flow in the background. The flow blocks in Wait() until
	// commander delivers the callback.
	go func() {
		err := oauth.RunLoginFlow(context.Background(), mcpName, spec.URL, source)
		if err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	select {
	case u := <-urlCh:
		return u, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
