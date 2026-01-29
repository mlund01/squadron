package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	provider      Provider
	model         string
	systemPrompts []string
	messages      []Message
	debugFile     *os.File
	stopSequences []string
}

func NewSession(provider Provider, model string, systemPrompts ...string) *Session {
	return &Session{
		provider:      provider,
		model:         model,
		systemPrompts: systemPrompts,
		messages:      []Message{},
	}
}

// EnableDebug opens a debug file for logging all messages
func (s *Session) EnableDebug(filename string) error {
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	s.debugFile = f

	// Log existing system prompts
	for i, prompt := range s.systemPrompts {
		s.logMessage(fmt.Sprintf("System Prompt %d", i+1), prompt)
	}

	return nil
}

// Close closes any open resources
func (s *Session) Close() {
	if s.debugFile != nil {
		s.debugFile.Close()
	}
}

func (s *Session) logMessage(label string, content string) {
	if s.debugFile == nil {
		return
	}
	timestamp := time.Now().Format(time.RFC3339)
	s.debugFile.WriteString(fmt.Sprintf("[%s] === %s ===\n", timestamp, label))
	s.debugFile.WriteString(content)
	s.debugFile.WriteString("\n\n")
}

func (s *Session) AddSystemPrompt(prompt string) {
	s.systemPrompts = append(s.systemPrompts, prompt)
	// Log the new system prompt if debug is enabled
	s.logMessage(fmt.Sprintf("System Prompt %d", len(s.systemPrompts)), prompt)
}

func (s *Session) SetStopSequences(sequences []string) {
	s.stopSequences = sequences
}

func (s *Session) GetHistory() []Message {
	return s.messages
}

func (s *Session) buildMessages(userMessage string) []Message {
	var msgs []Message

	// Add system prompts first
	for _, sp := range s.systemPrompts {
		msgs = append(msgs, Message{Role: RoleSystem, Content: sp})
	}

	// Add conversation history
	msgs = append(msgs, s.messages...)

	// Add the new user message
	msgs = append(msgs, Message{Role: RoleUser, Content: userMessage})

	return msgs
}

func (s *Session) Send(ctx context.Context, userMessage string) (*ChatResponse, error) {
	s.logMessage("User Message", userMessage)

	req := &ChatRequest{
		Model:         s.model,
		Messages:      s.buildMessages(userMessage),
		StopSequences: s.stopSequences,
	}

	resp, err := s.provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	s.logMessage("LLM Response", resp.Content)

	// Append user message and assistant response to history
	s.messages = append(s.messages, Message{Role: RoleUser, Content: userMessage})
	s.messages = append(s.messages, Message{Role: RoleAssistant, Content: resp.Content})

	return resp, nil
}

func (s *Session) SendStream(ctx context.Context, userMessage string, onChunk func(StreamChunk)) (*ChatResponse, error) {
	s.logMessage("User Message", userMessage)

	req := &ChatRequest{
		Model:         s.model,
		Messages:      s.buildMessages(userMessage),
		StopSequences: s.stopSequences,
	}

	stream, err := s.provider.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}

	var contentBuilder strings.Builder
	var lastChunk StreamChunk

	for chunk := range stream {
		if chunk.Error != nil {
			return nil, chunk.Error
		}

		contentBuilder.WriteString(chunk.Content)

		if onChunk != nil {
			onChunk(chunk)
		}

		lastChunk = chunk
	}

	content := contentBuilder.String()

	s.logMessage("LLM Response", content)

	// Build the final response
	resp := &ChatResponse{
		ID:      uuid.New().String(),
		Content: content,
	}

	// If the provider included finish reason in the last chunk, we'd capture it here
	_ = lastChunk // Provider implementations can extend this

	// Append user message and assistant response to history
	s.messages = append(s.messages, Message{Role: RoleUser, Content: userMessage})
	s.messages = append(s.messages, Message{Role: RoleAssistant, Content: content})

	return resp, nil
}
