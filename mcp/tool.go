package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"squadron/aitools"
)

// mcpTool adapts an MCP server's tool to squadron's aitools.Tool interface.
// It holds a reference to the Client so it can forward CallTool invocations.
type mcpTool struct {
	client *Client
	info   *ToolInfo
}

func (t *mcpTool) ToolName() string              { return t.info.Name }
func (t *mcpTool) ToolDescription() string       { return t.info.Description }
func (t *mcpTool) ToolPayloadSchema() aitools.Schema { return t.info.Schema }

// Call invokes the tool on the MCP server. params is a JSON string (the agent's
// serialized arguments). The return value is a plain string suitable for an
// <OBSERVATION> block; errors are prefixed with "error: ".
//
// If the underlying transport is dead (subprocess crashed, HTTP connection
// dropped), Call transparently respawns it via ensureAlive and retries once.
// A second failure is surfaced as a tool error so the agent can react.
func (t *mcpTool) Call(ctx context.Context, params string) string {
	var args map[string]any
	if strings.TrimSpace(params) != "" {
		if err := json.Unmarshal([]byte(params), &args); err != nil {
			return "error: invalid JSON params for " + t.info.Name + ": " + err.Error()
		}
	}

	req := mcpproto.CallToolRequest{}
	req.Params.Name = t.info.Name
	req.Params.Arguments = args

	result, err := t.callOnce(ctx, req)
	if err != nil && isTransportError(err) {
		if respawnErr := t.client.ensureAlive(); respawnErr != nil {
			return "error: " + respawnErr.Error()
		}
		result, err = t.callOnce(ctx, req)
	}
	if err != nil {
		return "error: " + err.Error()
	}

	var out strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(mcpproto.TextContent); ok {
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(tc.Text)
		}
	}

	if result.IsError {
		if out.Len() == 0 {
			return "error: tool returned error with no content"
		}
		return "error: " + out.String()
	}
	return out.String()
}

// callOnce forwards a single CallTool request to the current transport. The
// client handle is read under the client's lock so a concurrent respawn can't
// swap it out mid-call.
func (t *mcpTool) callOnce(ctx context.Context, req mcpproto.CallToolRequest) (*mcpproto.CallToolResult, error) {
	t.client.mu.Lock()
	inner := t.client.inner
	t.client.mu.Unlock()
	if inner == nil {
		return nil, fmt.Errorf("mcp %q: transport not initialized", t.client.name)
	}
	return inner.CallTool(ctx, req)
}

// isTransportError returns true if the error looks like a dead transport
// rather than an application-level tool failure. The mcp-go client surfaces
// transport failures as wrapped stdlib errors — net, io.EOF, context.Canceled
// from the subprocess dying — while app-level errors are reported inside
// CallToolResult.IsError. We keep this check deliberately generous: the
// retry is bounded to one attempt, so a false positive just means we spend
// one extra ping before the second call fails too.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Deadline exceeded on CallTool is ambiguous (could be a slow tool),
		// but if the caller's ctx was still live we wouldn't have hit it —
		// letting the retry path run is cheap and recovers from a subprocess
		// hang that's eating our calls.
		return true
	}
	msg := err.Error()
	for _, needle := range []string{"broken pipe", "connection refused", "connection reset", "transport", "closed pipe", "EOF"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
