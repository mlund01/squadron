package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"squadron/llm"
	"squadron/store"
)

// SessionMessageLoader is the read-side surface of store.SessionStore that
// agent persistence helpers depend on. Declared here so callers don't need
// to import the full store interface for restore-only paths.
type SessionMessageLoader interface {
	GetStructuredMessages(sessionID string) ([]store.StructuredMessage, error)
}

// partFromContentBlock projects an llm.ContentBlock onto the atomic columns
// of session_message_parts. Each block type populates a different subset of
// fields; see store.MessagePart for the per-type field map.
func partFromContentBlock(b llm.ContentBlock) store.MessagePart {
	p := store.MessagePart{Type: string(b.Type)}
	switch b.Type {
	case llm.ContentTypeText:
		p.Text = b.Text
	case llm.ContentTypeImage:
		if b.ImageData != nil {
			p.ImageData = b.ImageData.Data
			p.ImageMediaType = b.ImageData.MediaType
		}
	case llm.ContentTypeToolUse:
		if b.ToolUse != nil {
			p.ToolUseID = b.ToolUse.ID
			p.ToolName = b.ToolUse.Name
			if len(b.ToolUse.Input) > 0 {
				p.ToolInputJSON = string(b.ToolUse.Input)
			}
			if len(b.ToolUse.ThoughtSignature) > 0 {
				p.ThoughtSignature = append([]byte(nil), b.ToolUse.ThoughtSignature...)
			}
		}
	case llm.ContentTypeToolResult:
		if b.ToolResult != nil {
			p.ToolUseID = b.ToolResult.ToolUseID
			p.Text = b.ToolResult.Content
			isErr := b.ToolResult.IsError
			p.IsError = &isErr
		}
	case llm.ContentTypeThinking:
		if b.Thinking != nil {
			p.Text = b.Thinking.Text
			p.ThinkingSignature = b.Thinking.Signature
			p.ThinkingRedactedData = b.Thinking.RedactedData
			p.ProviderID = b.Thinking.ProviderID
			p.EncryptedContent = b.Thinking.EncryptedContent
		}
	case llm.ContentTypeProviderRaw:
		if b.ProviderRaw != nil {
			p.ProviderName = b.ProviderRaw.Provider
			p.ProviderType = b.ProviderRaw.Type
			if len(b.ProviderRaw.Data) > 0 {
				p.ProviderDataJSON = string(b.ProviderRaw.Data)
			}
		}
	}
	return p
}

// contentBlockFromPart inverts partFromContentBlock. Returns an error if
// the row carries a Type the current build doesn't recognize, so we fail
// fast rather than silently dropping content on resume.
func contentBlockFromPart(p store.MessagePart) (llm.ContentBlock, error) {
	t := llm.ContentType(p.Type)
	b := llm.ContentBlock{Type: t}
	switch t {
	case llm.ContentTypeText:
		b.Text = p.Text
	case llm.ContentTypeImage:
		b.ImageData = &llm.ImageBlock{Data: p.ImageData, MediaType: p.ImageMediaType}
	case llm.ContentTypeToolUse:
		tu := &llm.ToolUseBlock{ID: p.ToolUseID, Name: p.ToolName}
		if p.ToolInputJSON != "" {
			tu.Input = json.RawMessage(p.ToolInputJSON)
		}
		if len(p.ThoughtSignature) > 0 {
			tu.ThoughtSignature = append([]byte(nil), p.ThoughtSignature...)
		}
		b.ToolUse = tu
	case llm.ContentTypeToolResult:
		tr := &llm.ToolResultBlock{ToolUseID: p.ToolUseID, Content: p.Text}
		if p.IsError != nil {
			tr.IsError = *p.IsError
		}
		b.ToolResult = tr
	case llm.ContentTypeThinking:
		b.Thinking = &llm.ThinkingBlock{
			Text:             p.Text,
			Signature:        p.ThinkingSignature,
			RedactedData:     p.ThinkingRedactedData,
			ProviderID:       p.ProviderID,
			EncryptedContent: p.EncryptedContent,
		}
	case llm.ContentTypeProviderRaw:
		pr := &llm.ProviderRawBlock{Provider: p.ProviderName, Type: p.ProviderType}
		if p.ProviderDataJSON != "" {
			pr.Data = json.RawMessage(p.ProviderDataJSON)
		}
		b.ProviderRaw = pr
	default:
		return llm.ContentBlock{}, fmt.Errorf("unknown content block type %q", p.Type)
	}
	return b, nil
}

// PartsFromMessage returns the storage-layer projection of an llm.Message.
// If the message has no Parts but does have Content, a single text part is
// synthesized — the wire format is always parts-shaped, even for plain text.
func PartsFromMessage(m llm.Message) []store.MessagePart {
	if !m.HasParts() {
		if m.Content == "" {
			return nil
		}
		return []store.MessagePart{{Type: string(llm.ContentTypeText), Text: m.Content}}
	}
	out := make([]store.MessagePart, 0, len(m.Parts))
	for _, b := range m.Parts {
		out = append(out, partFromContentBlock(b))
	}
	return out
}

// MessageFromStored rebuilds an llm.Message from its stored parts. For
// pre-migration rows that have no parts, falls back to interpreting the
// legacy Content audit string as a single text block (best-effort
// behavior matching today's resume fidelity).
func MessageFromStored(sm store.StructuredMessage) (llm.Message, error) {
	m := llm.Message{Role: llm.Role(sm.Role), Content: sm.Content}
	if len(sm.Parts) == 0 {
		return m, nil
	}
	parts := make([]llm.ContentBlock, 0, len(sm.Parts))
	for _, p := range sm.Parts {
		b, err := contentBlockFromPart(p)
		if err != nil {
			return llm.Message{}, err
		}
		parts = append(parts, b)
	}
	m.Parts = parts
	// Backward-compat for callers that read Message.Content directly.
	m.Content = m.GetTextContent()
	return m, nil
}

// LoadSessionMessages reads a stored session and rebuilds its message
// history with structured Parts populated. Pre-migration rows fall back
// to the legacy Content audit string.
func LoadSessionMessages(loader SessionMessageLoader, sessionID string) ([]llm.Message, error) {
	stored, err := loader.GetStructuredMessages(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Message, 0, len(stored))
	for _, sm := range stored {
		m, err := MessageFromStored(sm)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// AuditContentForMessage produces the human-readable audit string stored
// in session_messages.content. Format matches the pre-structured-storage
// markers so the command-center transcript view keeps rendering the
// same shape; thinking blocks are elided to avoid noisy transcripts.
func AuditContentForMessage(m llm.Message) string {
	if !m.HasParts() {
		return m.Content
	}
	var sb strings.Builder
	for i, p := range m.Parts {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch p.Type {
		case llm.ContentTypeText:
			sb.WriteString(p.Text)
		case llm.ContentTypeToolUse:
			if p.ToolUse != nil {
				input := string(p.ToolUse.Input)
				if input == "" {
					input = "{}"
				}
				sb.WriteString(fmt.Sprintf("[tool_use: %s(%s)]", p.ToolUse.Name, input))
			}
		case llm.ContentTypeToolResult:
			if p.ToolResult != nil {
				sb.WriteString(fmt.Sprintf("[tool_result:%s] %s", p.ToolResult.ToolUseID, p.ToolResult.Content))
			}
		case llm.ContentTypeImage:
			sb.WriteString("[image]")
		case llm.ContentTypeThinking:
			// Thinking is a continuity-only signal; we don't surface it in
			// the audit log to avoid noisy transcripts.
		case llm.ContentTypeProviderRaw:
			if p.ProviderRaw != nil {
				sb.WriteString(fmt.Sprintf("[provider_raw:%s/%s]", p.ProviderRaw.Provider, p.ProviderRaw.Type))
			}
		}
	}
	return sb.String()
}
