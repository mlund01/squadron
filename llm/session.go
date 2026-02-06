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

// GetSystemPrompts returns the session's system prompts
func (s *Session) GetSystemPrompts() []string {
	return s.systemPrompts
}

// GetStopSequences returns the session's stop sequences
func (s *Session) GetStopSequences() []string {
	return s.stopSequences
}

// SnapshotMessages returns the current message history for inspection.
// The returned slice shares the underlying array — do not modify.
func (s *Session) SnapshotMessages() []Message {
	return s.messages
}

// Clone creates a copy of this session with the same state (system prompts, messages, etc.)
// The clone can be used independently without affecting the original session.
// Note: The clone shares the same provider instance but has its own message history copy.
func (s *Session) Clone() *Session {
	// Copy system prompts
	systemPromptsCopy := make([]string, len(s.systemPrompts))
	copy(systemPromptsCopy, s.systemPrompts)

	// Deep copy messages (including Parts with ImageData and Metadata)
	messagesCopy := make([]Message, len(s.messages))
	for i, msg := range s.messages {
		messagesCopy[i] = Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
		if len(msg.Parts) > 0 {
			messagesCopy[i].Parts = make([]ContentBlock, len(msg.Parts))
			for j, part := range msg.Parts {
				messagesCopy[i].Parts[j] = ContentBlock{
					Type: part.Type,
					Text: part.Text,
				}
				if part.ImageData != nil {
					messagesCopy[i].Parts[j].ImageData = &ImageBlock{
						Data:      part.ImageData.Data,
						MediaType: part.ImageData.MediaType,
					}
				}
			}
		}
		if msg.Metadata != nil {
			messagesCopy[i].Metadata = &MessageMetadata{
				MessageID:    msg.Metadata.MessageID,
				ToolName:     msg.Metadata.ToolName,
				MessageIndex: msg.Metadata.MessageIndex,
				IsPrunable:   msg.Metadata.IsPrunable,
			}
		}
	}

	// Copy stop sequences
	stopSequencesCopy := make([]string, len(s.stopSequences))
	copy(stopSequencesCopy, s.stopSequences)

	return &Session{
		provider:      s.provider, // Shared - providers are thread-safe
		model:         s.model,
		systemPrompts: systemPromptsCopy,
		messages:      messagesCopy,
		stopSequences: stopSequencesCopy,
		debugFile:     nil, // Don't share debug file - clones are for isolated queries
	}
}

func (s *Session) buildMessages(userMessage string) []Message {
	return s.buildMessagesWithMessage(NewTextMessage(RoleUser, userMessage))
}

// buildMessagesWithMessage builds the full message list including a multimodal message
func (s *Session) buildMessagesWithMessage(userMsg Message) []Message {
	var msgs []Message

	// Add system prompts first
	for _, sp := range s.systemPrompts {
		msgs = append(msgs, Message{Role: RoleSystem, Content: sp})
	}

	// Add conversation history
	msgs = append(msgs, s.messages...)

	// Add the new user message
	msgs = append(msgs, userMsg)

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

	// Capture usage from the final chunk if provider included it
	if lastChunk.Usage != nil {
		resp.Usage = *lastChunk.Usage
	}

	// Append user message and assistant response to history
	s.messages = append(s.messages, Message{Role: RoleUser, Content: userMessage})
	s.messages = append(s.messages, Message{Role: RoleAssistant, Content: content})

	return resp, nil
}

// SendMessageStream sends a multimodal message and streams the response
// Use this for messages containing images or mixed content
func (s *Session) SendMessageStream(ctx context.Context, userMsg Message, onChunk func(StreamChunk)) (*ChatResponse, error) {
	// Log text content for debugging (images are not logged)
	s.logMessage("User Message", userMsg.GetTextContent())
	if userMsg.HasParts() {
		for _, part := range userMsg.Parts {
			if part.Type == ContentTypeImage && part.ImageData != nil {
				s.logMessage("User Message Image", fmt.Sprintf("[Image: %s, %d bytes]", part.ImageData.MediaType, len(part.ImageData.Data)))
			}
		}
	}

	req := &ChatRequest{
		Model:         s.model,
		Messages:      s.buildMessagesWithMessage(userMsg),
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

	// Capture usage from the final chunk if provider included it
	if lastChunk.Usage != nil {
		resp.Usage = *lastChunk.Usage
	}

	// Append user message and assistant response to history
	s.messages = append(s.messages, userMsg)
	s.messages = append(s.messages, Message{Role: RoleAssistant, Content: content})

	return resp, nil
}
