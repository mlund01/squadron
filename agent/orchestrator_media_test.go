package agent

import (
	"context"
	"testing"

	"squadron/aitools"
	"squadron/llm"
)

type fakeMediaTool struct{}

func (t *fakeMediaTool) ToolName() string        { return "view_thing" }
func (t *fakeMediaTool) ToolDescription() string { return "views a thing" }
func (t *fakeMediaTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{Type: aitools.TypeObject}
}
func (t *fakeMediaTool) Call(_ context.Context, _ string) string {
	return "text summary"
}
func (t *fakeMediaTool) CallMedia(_ context.Context, _ string) (string, []aitools.MediaBlock) {
	return "text summary", []aitools.MediaBlock{{Kind: aitools.MediaKindImage, MediaType: "image/png", Data: "QUJD"}}
}

func TestOrchestrator_MediaToolRoutesMediaToSession(t *testing.T) {
	session := &fakeSession{
		responses: []*llm.ChatResponse{
			toolUseResponse("tu-1", "view_thing", `{}`, "tool_use"),
			textResponse("<ANSWER>done</ANSWER>", "end_turn"),
		},
	}
	streamer := &mockStreamer{}
	o := newTestOrchestrator(session, streamer)
	o.tools["view_thing"] = &fakeMediaTool{}

	result, err := o.processTurn(context.Background(), "go", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Complete || result.Answer != "done" {
		t.Fatalf("expected complete answer=done, got %+v", result)
	}

	// The text summary becomes the tool_result; the interceptor is nil so it
	// passes through unchanged and never carries the base64 media.
	if len(session.toolResults) == 0 || session.toolResults[0][0].Content != "text summary" {
		t.Fatalf("expected text tool_result 'text summary', got %+v", session.toolResults)
	}

	// The media is routed to AddToolResultMedia as an image content block.
	if len(session.toolMedia) != 1 {
		t.Fatalf("expected one media batch, got %d", len(session.toolMedia))
	}
	if session.toolMedia[0][0].Type != llm.ContentTypeImage {
		t.Fatalf("expected image content block, got %s", session.toolMedia[0][0].Type)
	}
	if session.toolMedia[0][0].ImageData == nil || session.toolMedia[0][0].ImageData.MediaType != "image/png" {
		t.Fatalf("expected image/png media, got %+v", session.toolMedia[0][0].ImageData)
	}
}
