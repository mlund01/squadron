// Package mcp is the squadron consumer side of the Model Context Protocol.
// It parses `mcp "name" { ... }` HCL blocks into a Spec, loads the server
// (auto-installing when a source is declared), and exposes its tools through
// the same aitools.Tool interface used by native plugins.
package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	mcpgoclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"squadron/aitools"
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
// at Initialize time.
type Client struct {
	name  string
	inner *mcpgoclient.Client
	tools []*ToolInfo
}

var (
	registry   = make(map[string]*Client) // keyed by "name"
	registryMu sync.Mutex
)

// Load returns a Client for the given name, starting and initializing the
// underlying MCP server if it hasn't been loaded yet (or if a cached entry's
// liveness check fails). Calls are idempotent — the same name always yields
// the same process for the lifetime of this CLI invocation.
func Load(name string, spec Spec) (*Client, error) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if existing, ok := registry[name]; ok {
		if existing.alive() {
			return existing, nil
		}
		// Stale handle — close and evict before restarting.
		_ = existing.inner.Close()
		delete(registry, name)
	}

	inner, err := startTransport(name, spec)
	if err != nil {
		return nil, err
	}

	// Start is idempotent (see mcp-go's Client.Start). Stdio auto-starts in
	// NewStdioMCPClient; HTTP needs an explicit Start before Initialize.
	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := inner.Start(startCtx); err != nil {
		startCancel()
		_ = inner.Close()
		return nil, fmt.Errorf("mcp %q: start transport: %w", name, err)
	}
	startCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initReq := mcpproto.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpproto.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpproto.Implementation{Name: "squadron", Version: "dev"}
	if _, err := inner.Initialize(ctx, initReq); err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("mcp %q: initialize: %w", name, err)
	}

	listRes, err := inner.ListTools(ctx, mcpproto.ListToolsRequest{})
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("mcp %q: list tools: %w", name, err)
	}

	infos := make([]*ToolInfo, 0, len(listRes.Tools))
	for _, t := range listRes.Tools {
		infos = append(infos, &ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Schema:      convertSchema(t.InputSchema),
		})
	}

	c := &Client{name: name, inner: inner, tools: infos}
	registry[name] = c
	return c, nil
}

// startTransport resolves the spec into a concrete mcpgoclient.Client. For
// source-backed specs this triggers the auto-installer on first load.
func startTransport(name string, spec Spec) (*mcpgoclient.Client, error) {
	command, args, env, err := resolveSpawn(name, spec)
	if err != nil {
		return nil, err
	}

	if spec.URL != "" {
		c, err := mcpgoclient.NewStreamableHttpClient(spec.URL, transport.WithHTTPHeaders(spec.Headers))
		if err != nil {
			return nil, fmt.Errorf("mcp %q: start http transport: %w", name, err)
		}
		return c, nil
	}

	c, err := mcpgoclient.NewStdioMCPClient(command, env, args...)
	if err != nil {
		return nil, fmt.Errorf("mcp %q: start stdio transport: %w", name, err)
	}
	return c, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.inner.Ping(ctx) == nil
}

// ListTools returns the tool snapshot captured at Initialize time.
func (c *Client) ListTools() ([]*ToolInfo, error) {
	return c.tools, nil
}

// GetTool returns an aitools.Tool adapter for the named tool.
func (c *Client) GetTool(toolName string) (aitools.Tool, error) {
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
func (c *Client) Close() {
	if c.inner != nil {
		_ = c.inner.Close()
	}
}

// CloseAll shuts down every MCP server in the global registry. Called from
// cmd/root.go's defer and the SIGINT handler.
func CloseAll() {
	registryMu.Lock()
	defer registryMu.Unlock()
	for name, c := range registry {
		if c.inner != nil {
			_ = c.inner.Close()
		}
		delete(registry, name)
	}
}
