package llm

import (
	"encoding/json"
	"testing"
)

// TestConvertAssistantMessage_ReasoningEchoIncludesSummary regression-tests
// that prior reasoning items echoed via OfReasoning always include the
// `summary` field. The Responses API rejects items without it:
//
//	400 Bad Request — "Missing required parameter: 'input[N].summary'"
//
// gpt-5-mini routinely emits reasoning items with no summary text (just
// encrypted_content), so the echo path must default Summary to an empty
// slice rather than leaving it nil.
func TestConvertAssistantMessage_ReasoningEchoIncludesSummary(t *testing.T) {
	p := &OpenAIProvider{}

	messages := []Message{
		{
			Role: RoleAssistant,
			Parts: []ContentBlock{
				// No-summary case — only ProviderID + EncryptedContent.
				{Type: ContentTypeThinking, Thinking: &ThinkingBlock{
					ProviderID:       "rs_no_summary",
					EncryptedContent: "encblob_1",
				}},
				// With-summary case.
				{Type: ContentTypeThinking, Thinking: &ThinkingBlock{
					ProviderID:       "rs_with_summary",
					Text:             "I should fetch the URL.",
					EncryptedContent: "encblob_2",
				}},
				// Cross-provider thinking (no ProviderID) — should be dropped.
				{Type: ContentTypeThinking, Thinking: &ThinkingBlock{
					Text:      "anthropic-flavored",
					Signature: "sigA",
				}},
			},
		},
	}

	_, items := p.convertMessages(messages)
	if len(items) != 2 {
		t.Fatalf("expected 2 reasoning items (foreign one dropped), got %d", len(items))
	}

	for i, item := range items {
		rp := item.OfReasoning
		if rp == nil {
			t.Fatalf("item %d: expected OfReasoning, got nil", i)
		}
		// Round-trip via JSON to mirror what the SDK actually sends.
		raw, err := json.Marshal(rp)
		if err != nil {
			t.Fatalf("item %d: marshal: %v", i, err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("item %d: unmarshal: %v", i, err)
		}
		// `summary` must be present in the marshaled JSON, even when empty.
		if _, ok := got["summary"]; !ok {
			t.Fatalf("item %d: marshaled JSON missing required `summary` field: %s", i, raw)
		}
	}
}
