package mcp

import (
	"context"
	"encoding/json"
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

	result, err := t.client.inner.CallTool(ctx, req)
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
