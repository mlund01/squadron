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

// CompactSettings contains settings for context compaction
type CompactSettings struct {
	TokenLimit    int // Trigger compaction when input tokens exceed this
	TurnRetention int // Keep this many recent turns uncompacted
}

// Compact summarizes older conversation turns to reduce context size.
// It keeps the last N turns (where N = turnRetention) intact and replaces
// older messages with a compressed summary. System prompts are never affected.
// Returns the number of messages that were compacted.
func (s *Session) Compact(turnRetention int) int {
	// A turn is a user message + assistant response pair (2 messages)
	messagesToRetain := turnRetention * 2

	// If we don't have enough messages to compact, return early
	if len(s.messages) <= messagesToRetain {
		return 0
	}

	// Identify messages to compact (everything before the retained turns)
	compactEnd := len(s.messages) - messagesToRetain
	if compactEnd <= 0 {
		return 0
	}

	// Build a summary of the compacted messages
	summary := s.buildCompactionSummary(s.messages[:compactEnd])

	// Create the new message history: summary + retained turns
	newMessages := make([]Message, 0, 1+messagesToRetain)
	newMessages = append(newMessages, Message{
		Role:    RoleUser,
		Content: summary,
	})
	newMessages = append(newMessages, s.messages[compactEnd:]...)

	compactedCount := compactEnd
	s.messages = newMessages

	s.logMessage("Compaction", fmt.Sprintf("Compacted %d messages into summary. Retained last %d turns.", compactedCount, turnRetention))

	return compactedCount
}

// buildCompactionSummary creates a condensed summary of compacted messages
func (s *Session) buildCompactionSummary(messages []Message) string {
	var summary strings.Builder
	summary.WriteString("<COMPACTED_CONTEXT>\n")
	summary.WriteString("The following is a summary of earlier conversation history that has been compacted to reduce context size:\n\n")

	// CRITICAL: Preserve the most recent task message so the agent knows its current assignment.
	// - First user message is the original task (if no later tasks)
	// - Messages with <NEW_TASK> are follow-up assignments - keep only the LAST one
	var lastTask string
	for i, msg := range messages {
		if msg.Role != RoleUser {
			continue
		}
		content := msg.GetTextContent()

		// Skip tool results (observations)
		if strings.Contains(content, "<OBSERVATION>") {
			continue
		}

		// First user message is the original task
		if i == 0 {
			lastTask = content
			continue
		}

		// Later <NEW_TASK> messages override as the current task
		if strings.Contains(content, "<NEW_TASK>") {
			lastTask = content
		}
	}

	if lastTask != "" {
		summary.WriteString("**Current Task:**\n")
		summary.WriteString(lastTask)
		summary.WriteString("\n\n")
	}

	// Extract key information from the conversation
	var toolCalls []string
	var keyFindings []string

	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		content := msg.GetTextContent()

		// Extract tool calls from user messages (observations)
		if msg.Role == RoleUser && strings.Contains(content, "<OBSERVATION>") {
			// Try to identify what tool was called by looking at the previous assistant message
			if i > 0 && messages[i-1].Role == RoleAssistant {
				prevContent := messages[i-1].GetTextContent()
				if actionStart := strings.Index(prevContent, "<ACTION>"); actionStart != -1 {
					if actionEnd := strings.Index(prevContent[actionStart:], "</ACTION>"); actionEnd != -1 {
						toolName := prevContent[actionStart+8 : actionStart+actionEnd]
						toolCalls = append(toolCalls, toolName)
					}
				}
			}
		}

		// Extract answers from assistant messages
		if msg.Role == RoleAssistant {
			if answerStart := strings.Index(content, "<ANSWER>"); answerStart != -1 {
				if answerEnd := strings.Index(content[answerStart:], "</ANSWER>"); answerEnd != -1 {
					answer := content[answerStart+8 : answerStart+answerEnd]
					if len(answer) > 200 {
						answer = answer[:200] + "..."
					}
					keyFindings = append(keyFindings, strings.TrimSpace(answer))
				}
			}
		}
	}

	if len(toolCalls) > 0 {
		summary.WriteString("Tools used: ")
		summary.WriteString(strings.Join(uniqueStrings(toolCalls), ", "))
		summary.WriteString("\n\n")
	}

	if len(keyFindings) > 0 {
		summary.WriteString("Key findings:\n")
		for _, finding := range keyFindings {
			summary.WriteString("- ")
			summary.WriteString(finding)
			summary.WriteString("\n")
		}
	}

	if len(toolCalls) == 0 && len(keyFindings) == 0 {
		summary.WriteString("(Earlier conversation contained general reasoning and exploration)\n")
	}

	summary.WriteString("</COMPACTED_CONTEXT>")
	return summary.String()
}

// uniqueStrings returns unique strings preserving order
func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(strs))
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
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
