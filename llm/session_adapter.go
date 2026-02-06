package llm

import (
	"context"
)

// SessionAdapter wraps a Session to implement chat.LLMSession interface
type SessionAdapter struct {
	session *Session
}

// NewSessionAdapter creates an adapter for the given session
func NewSessionAdapter(session *Session) *SessionAdapter {
	return &SessionAdapter{session: session}
}

// SendStream implements chat.LLMSession interface
func (a *SessionAdapter) SendStream(ctx context.Context, userMessage string, onChunk func(content string)) (*ChatResponse, error) {
	return a.session.SendStream(ctx, userMessage, func(chunk StreamChunk) {
		onChunk(chunk.Content)
	})
}

// SendMessageStream sends a multimodal message and streams the response
func (a *SessionAdapter) SendMessageStream(ctx context.Context, msg Message, onChunk func(content string)) (*ChatResponse, error) {
	return a.session.SendMessageStream(ctx, msg, func(chunk StreamChunk) {
		onChunk(chunk.Content)
	})
}

// GetSession returns the underlying session (needed for pruning integration)
func (a *SessionAdapter) GetSession() *Session {
	return a.session
}
