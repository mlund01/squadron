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
func (a *SessionAdapter) SendStream(ctx context.Context, userMessage string, onChunk func(content string)) error {
	_, err := a.session.SendStream(ctx, userMessage, func(chunk StreamChunk) {
		onChunk(chunk.Content)
	})
	return err
}
