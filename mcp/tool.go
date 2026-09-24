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

func (t *mcpTool) ToolName() string                  { return t.info.Name }
func (t *mcpTool) ToolDescription() string           { return t.info.Description }
func (t *mcpTool) ToolPayloadSchema() aitools.Schema { return t.info.Schema }
func (t *mcpTool) ToolReplaySafe() bool              { return t.info.Idempotent }

// Call invokes the tool on the MCP server. params is a JSON string (the agent's
// serialized arguments). The return value is a plain string suitable for an
// <OBSERVATION> block; errors are prefixed with "error: ".
//
// If the underlying transport is dead (subprocess crashed, HTTP connection
// dropped), Call transparently respawns it via ensureAlive and retries once.
// A second failure is surfaced as a tool error so the agent can react.
func (t *mcpTool) Call(ctx context.Context, params string) string {
	text, _ := t.CallMedia(ctx, params)
	return text
}

// CallMedia preserves MCP image content as typed media instead of silently
// discarding it. Text-returning servers that embed image data URLs are also
// normalized here for compatibility with existing MCP implementations.
func (t *mcpTool) CallMedia(ctx context.Context, params string) (string, []aitools.MediaBlock) {
	var args map[string]any
	if strings.TrimSpace(params) != "" {
		if err := json.Unmarshal([]byte(params), &args); err != nil {
			return "error: invalid JSON params for " + t.info.Name + ": " + err.Error(), nil
		}
	}

	req := mcpproto.CallToolRequest{}
	req.Params.Name = t.info.Name
	req.Params.Arguments = args
	applyInvocationMetadata(ctx, &req)

	result, err := t.callOnce(ctx, req)
	if err != nil && isTransportError(err) {
		if respawnErr := t.client.ensureAlive(); respawnErr != nil {
			return "error: " + respawnErr.Error(), nil
		}
		result, err = t.callOnce(ctx, req)
	}
	if err != nil {
		return "error: " + err.Error(), nil
	}

	text, media := contentFromMCPResult(result)

	if result.IsError {
		if text == "" {
			return "error: tool returned error with no content", nil
		}
		return "error: " + text, nil
	}
	return text, media
}

func applyInvocationMetadata(ctx context.Context, req *mcpproto.CallToolRequest) {
	metadata, ok := aitools.InvocationMetadataFromContext(ctx)
	if !ok || metadata.IdempotencyKey == "" {
		return
	}
	req.Params.Meta = mcpproto.NewMetaFromMap(map[string]any{
		"squadron.io/idempotency-key": metadata.IdempotencyKey,
		"squadron.io/tool-use-id":     metadata.ToolUseID,
	})
}

func contentFromMCPResult(result *mcpproto.CallToolResult) (string, []aitools.MediaBlock) {
	var out strings.Builder
	var media []aitools.MediaBlock
	for _, c := range result.Content {
		switch content := c.(type) {
		case mcpproto.TextContent:
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(content.Text)
		case mcpproto.ImageContent:
			media = append(media, aitools.MediaBlock{Kind: aitools.MediaKindImage, MediaType: content.MIMEType, Data: content.Data})
		case *mcpproto.ImageContent:
			if content != nil {
				media = append(media, aitools.MediaBlock{Kind: aitools.MediaKindImage, MediaType: content.MIMEType, Data: content.Data})
			}
		}
	}

	extracted := aitools.ExtractImages(out.String())
	for _, image := range extracted.Images {
		media = append(media, aitools.MediaBlock{Kind: aitools.MediaKindImage, MediaType: image.MediaType, Data: image.Data})
	}
	return extracted.RemainingText, media
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
