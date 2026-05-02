package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestConvertMessages_ParallelToolResults regression-tests the parallel
// tool-result expansion in the Responses API path. A bundled tool-result
// user message with N parts must produce N separate function_call_output
// items so each tool_call_id is acknowledged independently.
func TestConvertMessages_ParallelToolResults(t *testing.T) {
	p := &OpenAIProvider{}

	messages := []Message{
		{
			Role: RoleAssistant,
			Parts: []ContentBlock{
				{Type: ContentTypeToolUse, ToolUse: &ToolUseBlock{ID: "call_1", Name: "read_file", Input: json.RawMessage(`{"path":"/a"}`)}},
				{Type: ContentTypeToolUse, ToolUse: &ToolUseBlock{ID: "call_2", Name: "read_file", Input: json.RawMessage(`{"path":"/b"}`)}},
				{Type: ContentTypeToolUse, ToolUse: &ToolUseBlock{ID: "call_3", Name: "read_file", Input: json.RawMessage(`{"path":"/c"}`)}},
			},
		},
		{
			Role: RoleUser,
			Parts: []ContentBlock{
				{Type: ContentTypeToolResult, ToolResult: &ToolResultBlock{ToolUseID: "call_1", Content: "content of /a"}},
				{Type: ContentTypeToolResult, ToolResult: &ToolResultBlock{ToolUseID: "call_2", Content: "content of /b"}},
				{Type: ContentTypeToolResult, ToolResult: &ToolResultBlock{ToolUseID: "call_3", Content: "content of /c"}},
			},
		},
	}

	_, items := p.convertMessages(messages)

	var functionCallIDs []string
	var functionCallOutputIDs []string
	for _, item := range items {
		if item.OfFunctionCall != nil {
			functionCallIDs = append(functionCallIDs, item.OfFunctionCall.CallID)
		}
		if item.OfFunctionCallOutput != nil {
			functionCallOutputIDs = append(functionCallOutputIDs, item.OfFunctionCallOutput.CallID)
		}
	}

	if len(functionCallIDs) != 3 {
		t.Fatalf("expected 3 function_call items, got %d: %v", len(functionCallIDs), functionCallIDs)
	}
	if len(functionCallOutputIDs) != 3 {
		t.Fatalf("expected 3 function_call_output items, got %d: %v", len(functionCallOutputIDs), functionCallOutputIDs)
	}

	want := []string{"call_1", "call_2", "call_3"}
	for i, id := range functionCallIDs {
		if id != want[i] {
			t.Errorf("function_call %d: got call_id=%q, want %q", i, id, want[i])
		}
	}
	for i, id := range functionCallOutputIDs {
		if id != want[i] {
			t.Errorf("function_call_output %d: got call_id=%q, want %q", i, id, want[i])
		}
	}
}

func TestConvertMessages_SingleToolResult(t *testing.T) {
	p := &OpenAIProvider{}

	messages := []Message{
		{
			Role: RoleAssistant,
			Parts: []ContentBlock{
				{Type: ContentTypeToolUse, ToolUse: &ToolUseBlock{ID: "call_solo", Name: "fetch", Input: json.RawMessage(`{}`)}},
			},
		},
		{
			Role: RoleUser,
			Parts: []ContentBlock{
				{Type: ContentTypeToolResult, ToolResult: &ToolResultBlock{ToolUseID: "call_solo", Content: "OK"}},
			},
		},
	}

	_, items := p.convertMessages(messages)

	var outputs int
	for _, item := range items {
		if item.OfFunctionCallOutput != nil {
			if item.OfFunctionCallOutput.CallID != "call_solo" {
				t.Errorf("function_call_output id = %q, want call_solo", item.OfFunctionCallOutput.CallID)
			}
			outputs++
		}
	}
	if outputs != 1 {
		t.Errorf("expected 1 function_call_output, got %d", outputs)
	}
}

func TestConvertMessages_TextUserMessage(t *testing.T) {
	p := &OpenAIProvider{}
	messages := []Message{
		{Role: RoleUser, Content: "hello"},
	}
	_, items := p.convertMessages(messages)
	if len(items) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(items))
	}
	if items[0].OfMessage == nil {
		t.Fatalf("expected easy-input message item, got %+v", items[0])
	}
	if items[0].OfMessage.Role != "user" {
		t.Errorf("expected role=user, got %q", items[0].OfMessage.Role)
	}
}

func TestConvertMessages_SystemPromptCollapsesToInstructions(t *testing.T) {
	p := &OpenAIProvider{}
	messages := []Message{
		{Role: RoleSystem, Content: "you are a helpful agent"},
		{Role: RoleSystem, Content: "always answer concisely"},
		{Role: RoleUser, Content: "hi"},
	}
	instructions, items := p.convertMessages(messages)

	if instructions == "" {
		t.Fatal("expected non-empty instructions from system messages")
	}
	if !strings.Contains(instructions, "helpful agent") || !strings.Contains(instructions, "concisely") {
		t.Errorf("expected both system messages in instructions, got %q", instructions)
	}
	// The system messages should NOT appear as input items.
	for _, item := range items {
		if item.OfMessage != nil && item.OfMessage.Role == "system" {
			t.Errorf("system message leaked into input items: %+v", item)
		}
	}
}
