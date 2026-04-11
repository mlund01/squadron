package llm

import (
	"encoding/json"
	"testing"
)

// TestConvertMessages_ParallelToolResults is a regression test for the
// "tool_calls must be followed by tool messages responding to each tool_call_id"
// bug. Before the fix, a bundled tool-result message with N parts was
// collapsed into a single OpenAI tool message (emitting only m.Parts[0]),
// so when the LLM emitted parallel tool calls in one turn, all but the first
// tool result were silently dropped and the next API call failed.
//
// The converter must expand N tool_result parts into N separate tool messages,
// each carrying its own tool_call_id.
func TestConvertMessages_ParallelToolResults(t *testing.T) {
	p := &OpenAIProvider{}

	messages := []Message{
		{
			Role: RoleAssistant,
			Parts: []ContentBlock{
				{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:    "call_1",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"/a"}`),
					},
				},
				{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:    "call_2",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"/b"}`),
					},
				},
				{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:    "call_3",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"/c"}`),
					},
				},
			},
		},
		{
			Role: RoleUser,
			Parts: []ContentBlock{
				{
					Type:       ContentTypeToolResult,
					ToolResult: &ToolResultBlock{ToolUseID: "call_1", Content: "content of /a"},
				},
				{
					Type:       ContentTypeToolResult,
					ToolResult: &ToolResultBlock{ToolUseID: "call_2", Content: "content of /b"},
				},
				{
					Type:       ContentTypeToolResult,
					ToolResult: &ToolResultBlock{ToolUseID: "call_3", Content: "content of /c"},
				},
			},
		},
	}

	out := p.convertMessages(messages)

	// Walk the output and collect every tool message.
	var toolMessages []string
	var assistantCount int
	for _, m := range out {
		if m.OfAssistant != nil {
			assistantCount++
			continue
		}
		if m.OfTool != nil {
			toolMessages = append(toolMessages, m.OfTool.ToolCallID)
		}
	}

	if assistantCount != 1 {
		t.Errorf("expected 1 assistant message, got %d", assistantCount)
	}
	if len(toolMessages) != 3 {
		t.Fatalf("expected 3 tool messages (one per parallel call), got %d: %v", len(toolMessages), toolMessages)
	}

	// Order must match the original tool_use order so the IDs line up.
	want := []string{"call_1", "call_2", "call_3"}
	for i, id := range toolMessages {
		if id != want[i] {
			t.Errorf("tool message %d: got id=%q, want %q", i, id, want[i])
		}
	}
}

// TestConvertMessages_SingleToolResult covers the simple case (single tool
// call → single tool result) — this is the most common path and must keep
// working after the parallel fix.
func TestConvertMessages_SingleToolResult(t *testing.T) {
	p := &OpenAIProvider{}

	messages := []Message{
		{
			Role: RoleAssistant,
			Parts: []ContentBlock{
				{
					Type:    ContentTypeToolUse,
					ToolUse: &ToolUseBlock{ID: "call_solo", Name: "fetch", Input: json.RawMessage(`{}`)},
				},
			},
		},
		{
			Role: RoleUser,
			Parts: []ContentBlock{
				{Type: ContentTypeToolResult, ToolResult: &ToolResultBlock{ToolUseID: "call_solo", Content: "OK"}},
			},
		},
	}

	out := p.convertMessages(messages)

	var toolCount int
	for _, m := range out {
		if m.OfTool != nil {
			if m.OfTool.ToolCallID != "call_solo" {
				t.Errorf("tool call id = %q, want call_solo", m.OfTool.ToolCallID)
			}
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected 1 tool message, got %d", toolCount)
	}
}

// TestConvertMessages_TextUserMessage ensures that plain text user messages
// (no parts) still collapse to a single UserMessage and are not touched by
// the tool-result expansion path.
func TestConvertMessages_TextUserMessage(t *testing.T) {
	p := &OpenAIProvider{}
	messages := []Message{
		{Role: RoleUser, Content: "hello"},
	}
	out := p.convertMessages(messages)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if out[0].OfUser == nil {
		t.Fatalf("expected user message, got %+v", out[0])
	}
}

// TestConvertMessages_MixedUserParts ensures that a user message containing
// tool results AND other content parts (e.g. a text block) does not take the
// tool-result-expansion branch — it should collapse into a single UserMessage
// with content parts, matching the original behavior.
func TestConvertMessages_MixedUserParts(t *testing.T) {
	p := &OpenAIProvider{}
	messages := []Message{
		{
			Role: RoleUser,
			Parts: []ContentBlock{
				{Type: ContentTypeToolResult, ToolResult: &ToolResultBlock{ToolUseID: "call_x", Content: "x"}},
				{Type: ContentTypeText, Text: "hey"},
			},
		},
	}
	out := p.convertMessages(messages)
	// Mixed content => NOT the allToolResults path, so we get a single
	// UserMessage (even if the tool_result content inside it is lossy — that's
	// a separate concern; here we just assert it's not fanned out).
	var toolCount, userCount int
	for _, m := range out {
		if m.OfTool != nil {
			toolCount++
		}
		if m.OfUser != nil {
			userCount++
		}
	}
	if toolCount != 0 {
		t.Errorf("mixed user parts should not emit tool messages, got %d", toolCount)
	}
	if userCount != 1 {
		t.Errorf("expected 1 user message, got %d", userCount)
	}
}
