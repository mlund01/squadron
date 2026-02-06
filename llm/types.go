package llm

import "context"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// ContentType identifies the type of content in a ContentBlock
type ContentType string

const (
	ContentTypeText  ContentType = "text"
	ContentTypeImage ContentType = "image"
)

// ImageBlock represents base64-encoded image data
type ImageBlock struct {
	Data      string // Base64-encoded data (without data URL prefix)
	MediaType string // MIME type: "image/png", "image/jpeg", "image/gif", "image/webp"
}

// ContentBlock represents a single piece of content (text or image)
type ContentBlock struct {
	Type      ContentType
	Text      string      // Used when Type == ContentTypeText
	ImageData *ImageBlock // Used when Type == ContentTypeImage
}

// MessageMetadata holds tracking information for message pruning
type MessageMetadata struct {
	MessageID    string // Unique identifier for this message
	ToolName     string // Tool that produced this result (empty for non-tool messages)
	MessageIndex int    // Position in message history when added
	IsPrunable   bool   // Whether this message can be pruned (true for tool results)
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

type StreamChunk struct {
	Content string
	Done    bool
	Error   error
	Usage   *Usage // Only populated on final chunk (Done=true)
}

type ChatRequest struct {
	Model         string
	Messages      []Message
	MaxTokens     int
	Temperature   float64
	StopSequences []string
}

type ChatResponse struct {
	ID           string
	Content      string
	FinishReason string
	Usage        Usage
}

type Usage struct {
	InputTokens  int
	OutputTokens int

	// Cache-related fields (provider-specific, may be zero if not supported)
	CacheCreationInputTokens int // Anthropic: tokens used to create new cache entry
	CacheReadInputTokens     int // Anthropic: tokens read from existing cache
	CachedTokens             int // OpenAI: tokens served from cache (prompt_tokens_details.cached_tokens)
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
}
