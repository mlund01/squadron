package store

// MessagePart is a single content block of a session message, expressed
// as the atomic columns of the session_message_parts table. This type is
// deliberately neutral — it mirrors the table schema row-for-row and does
// not depend on llm types, so the store package stays free of provider
// concerns. Callers that work with llm.ContentBlock convert at the boundary
// (see agent/persistence.go).
//
// Each Type uses a different subset of fields:
//
//   - "text"          → Text
//   - "image"         → ImageData, ImageMediaType
//   - "tool_use"      → ToolUseID, ToolName, ToolInputJSON, ThoughtSignature
//   - "tool_result"   → ToolUseID, Text, IsError
//   - "thinking"      → Text, ThinkingSignature, ThinkingRedactedData,
//                       ProviderID, EncryptedContent
//   - "provider_raw"  → ProviderName, ProviderType, ProviderDataJSON
//
// Pointers (*bool) are used where NULL must be distinguishable from the
// zero value (e.g. is_error on tool_result, where false is meaningful).
type MessagePart struct {
	Type string

	Text string

	ToolUseID        string
	ToolName         string
	ToolInputJSON    string
	ThoughtSignature []byte
	IsError          *bool

	ImageData      string
	ImageMediaType string

	ThinkingSignature    string
	ThinkingRedactedData string
	ProviderID           string
	EncryptedContent     string

	ProviderName     string
	ProviderType     string
	ProviderDataJSON string
}

// StructuredMessage is the result of GetStructuredMessages — a session
// message with its content blocks rebuilt from session_message_parts. The
// Content field is the legacy human-readable audit string from
// session_messages.content (used as a fallback when Parts is empty,
// e.g. for pre-migration rows).
type StructuredMessage struct {
	ID      int
	Role    string
	Content string
	Parts   []MessagePart
}
