// Package mcp is the squadron consumer side of the Model Context Protocol.
// It parses `mcp "name" { ... }` HCL blocks into a Spec, loads the server
// (auto-installing when a source is declared), and exposes its tools through
// the same aitools.Tool interface used by native plugins.
package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	mcpgoclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"squadron/aitools"
	"squadron/mcp/oauth"
)

// Spec describes one mcp block's desired configuration. Exactly one of
// Command, URL, or Source must be set.
type Spec struct {
	// Bare-command mode (no auto-install).
	Command string

	// HTTP transport mode.
	URL     string
	Headers map[string]string

	// Auto-install mode: either "npm:<pkg>" or "github.com/<owner>/<repo>".
	Source  string
	Version string
	Entry   string // optional; github source only

	// Common fields.
	Args []string
	Env  map[string]string
}

// Client is a live handle to a loaded MCP server. Its tool list is snapshotted
// at Initialize time. The Spec is retained so the client can respawn the
// underlying transport on demand if the subprocess dies mid-run — see
// ensureAlive.
type Client struct {
	name        string
	spec        Spec
	mu          sync.Mutex
	inner       *mcpgoclient.Client
	tools       []*ToolInfo
	stopRefresh context.CancelFunc // nil if no refresh loop is running
}

var (
	registry   = make(map[string]*Client) // keyed by "name"
	registryMu sync.Mutex
)

// DefaultLoadTimeout bounds Start + Initialize + ListTools when the caller
// has no deadline of its own.
const DefaultLoadTimeout = 10 * time.Second

func Load(name string, spec Spec) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultLoadTimeout)
	defer cancel()
	return LoadWithContext(ctx, name, spec)
}

// LoadWithContext honours ctx's deadline for every network hop.
func LoadWithContext(ctx context.Context, name string, spec Spec) (*Client, error) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if existing, ok := registry[name]; ok {
		if existing.alive() {
			return existing, nil
		}
		// Stale handle — close and evict before restarting.
		existing.Close()
		delete(registry, name)
	}

	inner, tools, err := bringUpTransport(ctx, name, spec)
	if err != nil {
		return nil, err
	}

	c := &Client{name: name, spec: spec, inner: inner, tools: tools}
	registry[name] = c

	// Start a background refresh loop for OAuth-enabled HTTP MCPs. The loop
	// refreshes the token before it expires and reconnects the transport
	// (important for SSE whose GET stream holds a stale bearer token).
	if spec.URL != "" && oauth.HasToken(name) {
		refreshCtx, cancel := context.WithCancel(context.Background())
		c.stopRefresh = cancel
		oauth.StartRefreshLoop(refreshCtx, name, spec.URL, func() {
			// Token was refreshed — reconnect the transport so the SSE
			// stream picks up the new bearer token.
			_ = c.reconnect()
		})
	}

	return c, nil
}

// bringUpTransport runs the full Start + Initialize + ListTools sequence.
// Shared by Load and ensureAlive so respawns go through identical startup.
func bringUpTransport(ctx context.Context, name string, spec Spec) (*mcpgoclient.Client, []*ToolInfo, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultLoadTimeout)
		defer cancel()
	}

	inner, err := startTransport(name, spec)
	if err != nil {
		return nil, nil, err
	}

	// SSE's Start spawns a reader goroutine tied to the ctx we pass.
	// Handing it the bounded load ctx kills the stream as soon as Load
	// returns, which makes every subsequent CallTool hang waiting for a
	// response over a dead stream. Use Background for stateful transports;
	// Client.Close() at process exit still tears the stream down via
	// mcp-go's stored cancel. Streamable HTTP is stateless per-request so
	// it's fine to keep using ctx there.
	startCtx := ctx
	if spec.URL != "" && isSSEURL(spec.URL) {
		startCtx = context.Background()
	}
	if err := inner.Start(startCtx); err != nil {
		_ = inner.Close()
		if authErr := classifyAuthError(name, spec.URL, err); authErr != nil {
			return nil, nil, authErr
		}
		return nil, nil, fmt.Errorf("mcp %q: start transport: %w", name, err)
	}

	initReq := mcpproto.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpproto.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpproto.Implementation{Name: "squadron", Version: "dev"}
	if _, err := inner.Initialize(ctx, initReq); err != nil {
		_ = inner.Close()
		if authErr := classifyAuthError(name, spec.URL, err); authErr != nil {
			return nil, nil, authErr
		}
		return nil, nil, fmt.Errorf("mcp %q: initialize: %w", name, err)
	}

	listRes, err := inner.ListTools(ctx, mcpproto.ListToolsRequest{})
	if err != nil {
		_ = inner.Close()
		return nil, nil, fmt.Errorf("mcp %q: list tools: %w", name, err)
	}

	tools := make([]*ToolInfo, 0, len(listRes.Tools))
	for _, t := range listRes.Tools {
		tools = append(tools, &ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Schema:      convertSchema(t.InputSchema),
		})
	}

	return inner, tools, nil
}

// startTransport resolves the spec into a concrete mcpgoclient.Client. For
// source-backed specs this triggers the auto-installer on first load.
func startTransport(name string, spec Spec) (*mcpgoclient.Client, error) {
	command, args, env, err := resolveSpawn(name, spec)
	if err != nil {
		return nil, err
	}

	if spec.URL != "" {
		return startHTTPTransport(name, spec)
	}

	c, err := mcpgoclient.NewStdioMCPClient(command, env, args...)
	if err != nil {
		return nil, fmt.Errorf("mcp %q: start stdio transport: %w", name, err)
	}
	return c, nil
}

// startHTTPTransport picks one of mcp-go's four HTTP-family constructors
// based on URL suffix (SSE if path ends in /sse) and stored-token presence.
// Engaging the OAuth client without a stored token would make mcp-go error
// out locally before any request leaves the machine, so anonymous MCPs
// take the plain client path.
func startHTTPTransport(name string, spec Spec) (*mcpgoclient.Client, error) {
	sse := isSSEURL(spec.URL)
	_, hasStaticAuth := spec.Headers["Authorization"]
	useOAuth := !hasStaticAuth && oauth.HasToken(name)

	var oauthCfg transport.OAuthConfig
	if useOAuth {
		oauthCfg = transport.OAuthConfig{
			TokenStore:  oauth.NewVaultTokenStore(name),
			PKCEEnabled: true,
		}
		if creds, err := oauth.LoadClientCredentials(name); err == nil && creds != nil {
			oauthCfg.ClientID = creds.ClientID
			oauthCfg.ClientSecret = creds.ClientSecret
		}
		// Work around mcp-go's broken .well-known URL construction (it
		// appends the MCP path as a suffix, breaking RFC 8615). Do our
		// own origin-level discovery and pass the result explicitly.
		if metaURL, err := oauth.DiscoverAuthServerMetadataURL(context.Background(), spec.URL); err == nil {
			oauthCfg.AuthServerMetadataURL = metaURL
		}
	}

	var (
		c   *mcpgoclient.Client
		err error
	)
	switch {
	case sse && useOAuth:
		c, err = mcpgoclient.NewOAuthSSEClient(spec.URL, oauthCfg, transport.WithHeaders(spec.Headers))
	case sse:
		c, err = mcpgoclient.NewSSEMCPClient(spec.URL, transport.WithHeaders(spec.Headers))
	case useOAuth:
		c, err = mcpgoclient.NewOAuthStreamableHttpClient(spec.URL, oauthCfg, transport.WithHTTPHeaders(spec.Headers))
	default:
		c, err = mcpgoclient.NewStreamableHttpClient(spec.URL, transport.WithHTTPHeaders(spec.Headers))
	}
	if err != nil {
		return nil, fmt.Errorf("mcp %q: start http transport: %w", name, err)
	}
	return c, nil
}

// isSSEURL returns true if the URL's path suffix is "/sse". Heuristic —
// false positives surface as a loud mcp-go startup error.
func isSSEURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.TrimSuffix(u.Path, "/")
	return strings.HasSuffix(p, "/sse")
}

// resolveSpawn picks the final (command, args, env) to hand to the stdio
// transport. For source-backed specs it drives the installer.
func resolveSpawn(name string, spec Spec) (string, []string, []string, error) {
	env := envMapToList(spec.Env)

	if spec.URL != "" {
		return "", nil, nil, nil
	}
	if spec.Command != "" {
		return spec.Command, spec.Args, env, nil
	}
	if spec.Source == "" {
		return "", nil, nil, fmt.Errorf("mcp %q: need command, url, or source", name)
	}

	cfg, err := resolveRunner(name, spec.Version, spec.Source, spec.Entry)
	if err != nil {
		return "", nil, nil, err
	}
	if cfg.Runtime != "" {
		args := make([]string, 0, 1+len(spec.Args))
		args = append(args, cfg.Entry)
		args = append(args, spec.Args...)
		return cfg.Runtime, args, env, nil
	}
	return cfg.Entry, spec.Args, env, nil
}

func envMapToList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// alive pings the underlying transport to detect dead subprocesses. A failed
// ping causes Load to evict the cached entry and restart.
func (c *Client) alive() bool {
	c.mu.Lock()
	inner := c.inner
	c.mu.Unlock()
	if inner == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return inner.Ping(ctx) == nil
}

// ensureAlive pings the underlying transport and, if the ping fails, tears
// down the dead client and spawns a fresh one using the original spec. This
// lets long-running missions recover from a crashed stdio subprocess or a
// blipped HTTP connection without requiring a full config reload.
//
// The restart goes through bringUpTransport, which re-runs Initialize and
// ListTools. If the server's tool set has drifted across the restart, the
// in-memory snapshot is refreshed.
func (c *Client) ensureAlive() error {
	if c.alive() {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-check under the lock so concurrent callers collapse into a single
	// respawn attempt.
	if c.inner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		alive := c.inner.Ping(ctx) == nil
		cancel()
		if alive {
			return nil
		}
		_ = c.inner.Close()
		c.inner = nil
	}

	respawnCtx, cancel := context.WithTimeout(context.Background(), DefaultLoadTimeout)
	defer cancel()
	inner, tools, err := bringUpTransport(respawnCtx, c.name, c.spec)
	if err != nil {
		return fmt.Errorf("mcp %q: respawn failed: %w", c.name, err)
	}
	c.inner = inner
	c.tools = tools
	return nil
}

// ListTools returns the tool snapshot captured at Initialize time (or after
// the most recent respawn).
func (c *Client) ListTools() ([]*ToolInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools, nil
}

// GetTool returns an aitools.Tool adapter for the named tool.
func (c *Client) GetTool(toolName string) (aitools.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.tools {
		if t.Name == toolName {
			return &mcpTool{client: c, info: t}, nil
		}
	}
	return nil, fmt.Errorf("mcp %q: tool %q not found", c.name, toolName)
}

// GetAllTools returns every tool this server exposes, keyed by its original
// name. Callers usually prefix the keys with "mcp.<name>." before merging.
func (c *Client) GetAllTools() (map[string]aitools.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]aitools.Tool, len(c.tools))
	for _, t := range c.tools {
		out[t.Name] = &mcpTool{client: c, info: t}
	}
	return out, nil
}

// Name returns the squadron-side name of this MCP server (the HCL block label).
func (c *Client) Name() string { return c.name }

// Close shuts down this client. Prefer CloseAll() at program exit rather than
// per-client cleanup.
// reconnect tears down the current transport and brings up a fresh one.
// Called by the refresh loop after a token refresh so SSE streams pick up
// the new bearer token.
func (c *Client) reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner != nil {
		_ = c.inner.Close()
		c.inner = nil
	}
	respawnCtx, cancel := context.WithTimeout(context.Background(), DefaultLoadTimeout)
	defer cancel()
	inner, tools, err := bringUpTransport(respawnCtx, c.name, c.spec)
	if err != nil {
		return err
	}
	c.inner = inner
	c.tools = tools
	return nil
}

func (c *Client) Close() {
	if c.stopRefresh != nil {
		c.stopRefresh()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner != nil {
		_ = c.inner.Close()
		c.inner = nil
	}
}

// CloseAll shuts down every MCP server in the global registry. Called from
// cmd/root.go's defer and the SIGINT handler.
func CloseAll() {
	registryMu.Lock()
	defer registryMu.Unlock()
	for name, c := range registry {
		c.Close()
		delete(registry, name)
	}
}
