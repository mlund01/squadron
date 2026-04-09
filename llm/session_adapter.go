package llm

import (
	"context"
)

// SessionAdapter wraps a Session to implement the llmSession interface used by the orchestrator
type SessionAdapter struct {
	session *Session
}

// NewSessionAdapter creates an adapter for the given session
func NewSessionAdapter(session *Session) *SessionAdapter {
	return &SessionAdapter{session: session}
}

// SendStream implements llmSession interface with StreamChunk callback
func (a *SessionAdapter) SendStream(ctx context.Context, userMessage string, onChunk func(chunk StreamChunk)) (*ChatResponse, error) {
	return a.session.SendStream(ctx, userMessage, onChunk)
}

// SendMessageStream sends a multimodal message and streams the response
func (a *SessionAdapter) SendMessageStream(ctx context.Context, msg Message, onChunk func(chunk StreamChunk)) (*ChatResponse, error) {
	return a.session.SendMessageStream(ctx, msg, onChunk)
}

// ContinueStream resumes from existing session state without adding a new user message
func (a *SessionAdapter) ContinueStream(ctx context.Context, onChunk func(chunk StreamChunk)) (*ChatResponse, error) {
	return a.session.ContinueStream(ctx, onChunk)
}

// AddToolResults appends tool result messages to the session history
func (a *SessionAdapter) AddToolResults(results []ToolResultBlock) {
	a.session.AddToolResults(results)
}

// GetSession returns the underlying session (needed for pruning integration)
func (a *SessionAdapter) GetSession() *Session {
	return a.session
}
