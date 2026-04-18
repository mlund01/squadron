package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// ContentType identifies the type of content in a ContentBlock
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImage      ContentType = "image"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

// ImageBlock represents base64-encoded image data
type ImageBlock struct {
	Data      string // Base64-encoded data (without data URL prefix)
	MediaType string // MIME type: "image/png", "image/jpeg", "image/gif", "image/webp"
}

// ToolDefinition is a provider-agnostic tool definition passed in API requests
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolUseBlock represents the model requesting a tool call
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// ThoughtSignature is an opaque signature that Gemini thinking models attach
	// to function call parts. It must be round-tripped back in history for
	// subsequent requests; omitting it causes a 400 "missing thought_signature".
	// Empty for non-Gemini providers.
	ThoughtSignature []byte `json:"thought_signature,omitempty"`
}

// ToolResultBlock represents the result of a tool call
type ToolResultBlock struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ContentBlock represents a single piece of content (text, image, tool use, or tool result)
type ContentBlock struct {
	Type       ContentType
	Text       string           // Used when Type == ContentTypeText
	ImageData  *ImageBlock      // Used when Type == ContentTypeImage
	ToolUse    *ToolUseBlock    // Used when Type == ContentTypeToolUse
	ToolResult *ToolResultBlock // Used when Type == ContentTypeToolResult
}

// MessageMetadata holds tracking information for messages
type MessageMetadata struct {
	MessageID    string // Unique identifier for this message
	ToolName     string // Tool that produced this result (empty for non-tool messages)
	MessageIndex int    // Position in message history when added
}

// Message represents a conversation message with optional multimodal content
type Message struct {
	Role     Role
	Content  string           // Simple text content (for backward compatibility)
	Parts    []ContentBlock   // Multimodal content blocks (takes precedence over Content if non-empty)
	Metadata *MessageMetadata // Optional metadata for pruning tracking
}

// HasParts returns true if the message has multimodal content blocks
func (m Message) HasParts() bool {
	return len(m.Parts) > 0
}

// GetTextContent returns the text content of the message
// If Parts is set, concatenates all text parts; otherwise returns Content
func (m Message) GetTextContent() string {
	if !m.HasParts() {
		return m.Content
	}
	var text string
	for _, part := range m.Parts {
		if part.Type == ContentTypeText {
			text += part.Text
		}
	}
	return text
}

// NewTextMessage creates a simple text-only message
func NewTextMessage(role Role, text string) Message {
	return Message{Role: role, Content: text}
}

// NewImageMessage creates a message containing only an image
func NewImageMessage(role Role, image *ImageBlock) Message {
	return Message{
		Role: role,
		Parts: []ContentBlock{
			{Type: ContentTypeImage, ImageData: image},
		},
	}
}

// NewMultimodalMessage creates a message with multiple content blocks
func NewMultimodalMessage(role Role, parts ...ContentBlock) Message {
	return Message{Role: role, Parts: parts}
}

// ToolCallStartChunk signals that a new tool_use block is starting in the stream
type ToolCallStartChunk struct {
	ID   string
	Name string
}

type StreamChunk struct {
	Content string
	Done    bool
	Error   error
	Usage   *Usage // Only populated on final chunk (Done=true)

	// Tool call streaming fields
	ToolCallStart *ToolCallStartChunk // Signals a new tool_use block starting
	ToolCallDelta string              // Incremental JSON input for the current tool call
	ToolCallDone  *string             // Tool call ID when its input is complete

	// Final response metadata (populated on Done=true)
	StopReason    string         // "end_turn", "tool_use", "stop_sequence", etc.
	ContentBlocks []ContentBlock // Accumulated structured content blocks
}

type ChatRequest struct {
	Model               string
	Messages            []Message
	MaxTokens           int
	Temperature         float64
	StopSequences       []string
	PromptCaching       bool // Cache system prompts
	ConversationCaching bool // Cache conversation history (last user message breakpoint)
	Tools               []ToolDefinition // Tool definitions for native tool calling
}

type ChatResponse struct {
	ID            string
	Content       string         // Text content (backward compat: concatenation of text blocks)
	ContentBlocks []ContentBlock // Full structured response (text + tool_use blocks)
	FinishReason  string
	Usage         Usage
}

type Usage struct {
	InputTokens  int
	OutputTokens int

	CacheWriteTokens int // Tokens written to cache (Anthropic: cache_creation_input_tokens)
	CacheReadTokens  int // Tokens read from cache (Anthropic: cache_read_input_tokens, OpenAI: cached_tokens)
}

// Total is the sum of all token categories — used by budget accounting where
// every category counts against the same cap.
func (u Usage) Total() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
}
