package agent

import (
	"encoding/json"
	"testing"

	"squadron/llm"
	"squadron/store"
)

func TestPartFromContentBlockRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		block llm.ContentBlock
	}{
		{
			name:  "text",
			block: llm.ContentBlock{Type: llm.ContentTypeText, Text: "hello"},
		},
		{
			name: "image",
			block: llm.ContentBlock{Type: llm.ContentTypeImage, ImageData: &llm.ImageBlock{
				Data: "AAAA", MediaType: "image/png",
			}},
		},
		{
			name: "tool_use_with_signature",
			block: llm.ContentBlock{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
				ID:               "call_1",
				Name:             "do",
				Input:            json.RawMessage(`{"a":1}`),
				ThoughtSignature: []byte{0x01, 0x02},
			}},
		},
		{
			name: "tool_result_error",
			block: llm.ContentBlock{Type: llm.ContentTypeToolResult, ToolResult: &llm.ToolResultBlock{
				ToolUseID: "call_1",
				Content:   "boom",
				IsError:   true,
			}},
		},
		{
			name: "tool_result_ok",
			block: llm.ContentBlock{Type: llm.ContentTypeToolResult, ToolResult: &llm.ToolResultBlock{
				ToolUseID: "call_2",
				Content:   "ok",
				IsError:   false,
			}},
		},
		{
			name: "thinking_anthropic",
			block: llm.ContentBlock{Type: llm.ContentTypeThinking, Thinking: &llm.ThinkingBlock{
				Text:      "I think",
				Signature: "sig",
			}},
		},
		{
			name: "thinking_openai_with_encrypted",
			block: llm.ContentBlock{Type: llm.ContentTypeThinking, Thinking: &llm.ThinkingBlock{
				Text:             "summary",
				ProviderID:       "rs_xyz",
				EncryptedContent: "ciphertext",
			}},
		},
		{
			name: "thinking_anthropic_redacted",
			block: llm.ContentBlock{Type: llm.ContentTypeThinking, Thinking: &llm.ThinkingBlock{
				RedactedData: "encrypted_envelope",
			}},
		},
		{
			name: "provider_raw",
			block: llm.ContentBlock{Type: llm.ContentTypeProviderRaw, ProviderRaw: &llm.ProviderRawBlock{
				Provider: "anthropic",
				Type:     "server_tool_use",
				Data:     json.RawMessage(`{"k":"v"}`),
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := partFromContentBlock(tc.block)
			got, err := contentBlockFromPart(row)
			if err != nil {
				t.Fatalf("contentBlockFromPart: %v", err)
			}
			if got.Type != tc.block.Type {
				t.Fatalf("Type: got %q want %q", got.Type, tc.block.Type)
			}
			// Compare nested structs by field — ToolUse.Input is json.RawMessage,
			// reflect.DeepEqual works because we round-trip the bytes verbatim.
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.block)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("round-trip mismatch:\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestMessageFromStoredFallsBackToContent(t *testing.T) {
	// Pre-migration row: Content set, Parts empty.
	sm := store.StructuredMessage{
		Role:    "user",
		Content: "legacy",
	}
	m, err := MessageFromStored(sm)
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != llm.RoleUser {
		t.Fatalf("role: got %q want user", m.Role)
	}
	if m.Content != "legacy" {
		t.Fatalf("content: got %q want legacy", m.Content)
	}
	if m.HasParts() {
		t.Fatalf("expected no parts for legacy row, got %v", m.Parts)
	}
}

func TestMessageFromStoredRebuildsParts(t *testing.T) {
	sm := store.StructuredMessage{
		Role: "assistant",
		Parts: []store.MessagePart{
			{Type: "text", Text: "hello "},
			{Type: "text", Text: "world"},
			{Type: "tool_use", ToolUseID: "c1", ToolName: "fn", ToolInputJSON: `{"x":1}`},
		},
	}
	m, err := MessageFromStored(sm)
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasParts() {
		t.Fatal("expected parts")
	}
	if len(m.Parts) != 3 {
		t.Fatalf("parts len: got %d want 3", len(m.Parts))
	}
	// Synthesized text-only Content concatenates text parts.
	if m.Content != "hello world" {
		t.Fatalf("content: got %q want %q", m.Content, "hello world")
	}
	if m.Parts[2].ToolUse == nil || m.Parts[2].ToolUse.ID != "c1" {
		t.Fatalf("tool_use round-trip lost: %+v", m.Parts[2])
	}
}
