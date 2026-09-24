package mcp

import (
	"context"
	"testing"

	mcpproto "github.com/mark3labs/mcp-go/mcp"

	"squadron/aitools"
)

func TestContentFromMCPResultPreservesTypedAndEmbeddedImages(t *testing.T) {
	result := &mcpproto.CallToolResult{Content: []mcpproto.Content{
		mcpproto.TextContent{Type: "text", Text: "first"},
		mcpproto.ImageContent{Type: "image", MIMEType: "image/jpeg", Data: "/9j/AAAA"},
		mcpproto.TextContent{Type: "text", Text: "data:image/png;base64,iVBORw0KGgoAAA=="},
	}}

	text, media := contentFromMCPResult(result)
	if text != "first\n[image]" {
		t.Fatalf("text = %q", text)
	}
	if len(media) != 2 {
		t.Fatalf("media count = %d, want 2", len(media))
	}
	if media[0].MediaType != "image/jpeg" || media[0].Data != "/9j/AAAA" {
		t.Fatalf("typed image was not preserved: %#v", media[0])
	}
	if media[1].MediaType != "image/png" || media[1].Data != "iVBORw0KGgoAAA==" {
		t.Fatalf("embedded image was not promoted: %#v", media[1])
	}
}

func TestApplyInvocationMetadataUsesMCPMeta(t *testing.T) {
	t.Parallel()

	req := &mcpproto.CallToolRequest{}
	ctx := aitools.WithInvocationMetadata(context.Background(), aitools.InvocationMetadata{ToolUseID: "call-1", IdempotencyKey: "key-1"})
	applyInvocationMetadata(ctx, req)
	if req.Params.Meta == nil {
		t.Fatal("MCP request metadata is nil")
	}
	if got := req.Params.Meta.AdditionalFields["squadron.io/idempotency-key"]; got != "key-1" {
		t.Fatalf("MCP idempotency key = %#v, want key-1", got)
	}
	if got := req.Params.Meta.AdditionalFields["squadron.io/tool-use-id"]; got != "call-1" {
		t.Fatalf("MCP tool-use ID = %#v, want call-1", got)
	}
}
