package llm

import "context"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Message struct {
	Role    Role
	Content string
}

type StreamChunk struct {
	Content string
	Done    bool
	Error   error
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float64
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
}

type Provider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
}
