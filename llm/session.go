package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	tools         []ToolDefinition // Tool definitions for native tool calling
	promptCaching        bool
	conversationCaching  bool // Whether to cache conversation history (disabled when pruning is active)
}

func NewSession(provider Provider, model string, systemPrompts ...string) *Session {
	return &Session{
		provider:      provider,
		model:         model,
		systemPrompts: systemPrompts,
		messages:      []Message{},
	}
}

// SetPromptCaching enables or disables prompt caching for this session.
// When conversationCaching is true, a cache breakpoint is also set on conversation
// history (last user message). Disable this when pruning is active since the
// shifting message window invalidates the cache every turn.
func (s *Session) SetPromptCaching(enabled bool, conversationCaching bool) {
	s.promptCaching = enabled
	s.conversationCaching = conversationCaching
}

// retryableStatusCodes are HTTP status codes that indicate a transient error
// worth retrying: rate limits (429), server errors (5xx), and Anthropic
// overloaded (529).
var retryableStatusCodes = []string{"429", "500", "502", "503", "504", "529"}

// isRetryableError checks if an LLM provider error is transient and may
// succeed on retry. Works across providers by checking the error message
// for HTTP status codes (both OpenAI and Anthropic SDKs include them).
func isRetryableError(err error) bool {
	msg := err.Error()
	for _, code := range retryableStatusCodes {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

// retryBackoffs defines the exponential backoff schedule for retries.
var retryBackoffs = []time.Duration{2, 4, 8, 16, 32, 64}

// chatStreamWithRetry wraps provider.ChatStream with exponential backoff for
// transient errors (429, 5xx). Retries up to 6 times with 2, 4, 8, 16, 32, 64 second delays.
func (s *Session) chatStreamWithRetry(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	for attempt := 0; ; attempt++ {
		stream, err := s.provider.ChatStream(ctx, req)
		if err == nil {
			return stream, nil
		}

		if !isRetryableError(err) || attempt >= len(retryBackoffs) {
			return nil, err
		}

		backoff := retryBackoffs[attempt] * time.Second
		log.Printf("[LLM] Retryable error (attempt %d/%d: %v), retrying in %s...", attempt+1, len(retryBackoffs), err, backoff)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
}

// streamWithRetry handles the full stream lifecycle with retries.
// Connection and mid-stream errors are retried with backoff.
// On retry, the onChunk callback is suppressed to avoid sending
// duplicate/garbled chunks to the UI — only the final successful
// stream delivers chunks.
func (s *Session) streamWithRetry(ctx context.Context, req *ChatRequest, onChunk func(StreamChunk)) (streamResult, error) {
	for attempt := 0; ; attempt++ {
		stream, err := s.provider.ChatStream(ctx, req)
		if err != nil {
			if !isRetryableError(err) || attempt >= len(retryBackoffs) {
				return streamResult{}, err
			}
			backoff := retryBackoffs[attempt] * time.Second
			log.Printf("[LLM] Retryable connection error (attempt %d/%d: %v), retrying in %s...", attempt+1, len(retryBackoffs), err, backoff)
			select {
			case <-ctx.Done():
				return streamResult{}, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}

		// On first attempt, use the real callback. On retries, suppress
		// chunks since the UI already received partial data from the failed attempt.
		cb := onChunk
		if attempt > 0 {
			cb = nil
		}

		sr, streamErr := readStream(ctx, stream, cb)
		if streamErr == nil {
			return sr, nil
		}

		if !isRetryableError(streamErr) || attempt >= len(retryBackoffs) {
			return streamResult{}, streamErr
		}

		backoff := retryBackoffs[attempt] * time.Second
		log.Printf("[LLM] Retryable stream error (attempt %d/%d: %v), retrying in %s...", attempt+1, len(retryBackoffs), streamErr, backoff)
		select {
		case <-ctx.Done():
			return streamResult{}, ctx.Err()
		case <-time.After(backoff):
		}
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

// SetTools configures tool definitions for this session.
// Tools are automatically included in all chat requests.
func (s *Session) SetTools(tools []ToolDefinition) {
	s.tools = tools
}

// GetTools returns the session's configured tool definitions
func (s *Session) GetTools() []ToolDefinition {
	return s.tools
}

// AddToolResults appends a user message with tool result content blocks.
// Each result corresponds to a tool_use block from the previous assistant message.
func (s *Session) AddToolResults(results []ToolResultBlock) {
	parts := make([]ContentBlock, len(results))
	for i, r := range results {
		parts[i] = ContentBlock{
			Type:       ContentTypeToolResult,
			ToolResult: &r,
		}
	}
	s.messages = append(s.messages, Message{
		Role:  RoleUser,
		Parts: parts,
	})
}

// stripStopSequences removes any stop sequence text from response content.
// When models don't support the 'stop' API parameter, they may output the
// stop sequence literally. This ensures it's never stored in session history.
func (s *Session) stripStopSequences(content string) string {
	for _, seq := range s.stopSequences {
		content = strings.ReplaceAll(content, seq, "")
	}
	return content
}

func (s *Session) GetHistory() []Message {
	return s.messages
}

// LoadMessages replaces the session's message history with the provided messages.
// Messages with RoleSystem are loaded into systemPrompts; all others into messages.
// Used for restoring a session from persisted state (e.g., mission resume).
func (s *Session) LoadMessages(msgs []Message) {
	var systemPrompts []string
	var conversationMsgs []Message
	for _, m := range msgs {
		if m.Role == RoleSystem {
			systemPrompts = append(systemPrompts, m.Content)
		} else {
			conversationMsgs = append(conversationMsgs, m)
		}
	}
	if len(systemPrompts) > 0 {
		s.systemPrompts = systemPrompts
	}
	s.messages = conversationMsgs
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
				if part.ToolUse != nil {
					inputCopy := make(json.RawMessage, len(part.ToolUse.Input))
					copy(inputCopy, part.ToolUse.Input)
					messagesCopy[i].Parts[j].ToolUse = &ToolUseBlock{
						ID:    part.ToolUse.ID,
						Name:  part.ToolUse.Name,
						Input: inputCopy,
					}
				}
				if part.ToolResult != nil {
					messagesCopy[i].Parts[j].ToolResult = &ToolResultBlock{
						ToolUseID: part.ToolResult.ToolUseID,
						Content:   part.ToolResult.Content,
						IsError:   part.ToolResult.IsError,
					}
				}
			}
		}
		if msg.Metadata != nil {
			messagesCopy[i].Metadata = &MessageMetadata{
				MessageID:    msg.Metadata.MessageID,
				ToolName:     msg.Metadata.ToolName,
				MessageIndex: msg.Metadata.MessageIndex,
			}
		}
	}

	// Copy stop sequences
	stopSequencesCopy := make([]string, len(s.stopSequences))
	copy(stopSequencesCopy, s.stopSequences)

	// Copy tools
	toolsCopy := make([]ToolDefinition, len(s.tools))
	copy(toolsCopy, s.tools)

	return &Session{
		provider:      s.provider, // Shared - providers are thread-safe
		model:         s.model,
		systemPrompts: systemPromptsCopy,
		messages:      messagesCopy,
		stopSequences: stopSequencesCopy,
		tools:         toolsCopy,
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
		Tools:         s.tools,
		PromptCaching:       s.promptCaching,
		ConversationCaching: s.conversationCaching,
	}

	resp, err := s.provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	resp.Content = s.stripStopSequences(resp.Content)

	s.logMessage("LLM Response", resp.Content)

	// Append user message and assistant response to history
	s.messages = append(s.messages, Message{Role: RoleUser, Content: userMessage})
	s.messages = append(s.messages, s.buildAssistantMessage(resp.Content, resp.ContentBlocks))

	return resp, nil
}

// buildAssistantMessage constructs the assistant message for session history.
// If contentBlocks contain tool_use blocks, the message uses Parts for structured content.
// Otherwise, it falls back to simple text content.
func (s *Session) buildAssistantMessage(textContent string, contentBlocks []ContentBlock) Message {
	hasToolUse := false
	for _, b := range contentBlocks {
		if b.Type == ContentTypeToolUse {
			hasToolUse = true
			break
		}
	}

	if !hasToolUse {
		return Message{Role: RoleAssistant, Content: textContent}
	}

	// Build Parts: include text block(s) and tool_use blocks
	var parts []ContentBlock
	if textContent != "" {
		parts = append(parts, ContentBlock{Type: ContentTypeText, Text: textContent})
	}
	for _, b := range contentBlocks {
		if b.Type == ContentTypeToolUse {
			parts = append(parts, b)
		}
	}

	return Message{
		Role:    RoleAssistant,
		Content: textContent, // Keep for backward compat
		Parts:   parts,
	}
}

func (s *Session) SendStream(ctx context.Context, userMessage string, onChunk func(StreamChunk)) (*ChatResponse, error) {
	s.logMessage("User Message", userMessage)

	req := &ChatRequest{
		Model:         s.model,
		Messages:      s.buildMessages(userMessage),
		StopSequences: s.stopSequences,
		Tools:         s.tools,
		PromptCaching:       s.promptCaching,
		ConversationCaching: s.conversationCaching,
	}

	sr, err := s.streamWithRetry(ctx, req, onChunk)
	if err != nil {
		return nil, err
	}

	content := s.stripStopSequences(sr.TextContent)

	s.logMessage("LLM Response", content)

	// Build the final response
	resp := &ChatResponse{
		ID:            uuid.New().String(),
		Content:       content,
		ContentBlocks: sr.ContentBlocks,
		FinishReason:  sr.StopReason,
	}

	// Capture usage from the final chunk if provider included it
	if sr.LastChunk.Usage != nil {
		resp.Usage = *sr.LastChunk.Usage
	}

	// Append user message and assistant response to history
	s.messages = append(s.messages, Message{Role: RoleUser, Content: userMessage})
	s.messages = append(s.messages, s.buildAssistantMessage(content, sr.ContentBlocks))

	return resp, nil
}

// ContinueStream resumes from the current session state without adding a new user message.
// Unlike SendStream, it sends the existing history as-is and only appends the assistant
// response. Used when resuming an interrupted session where a pending user message is
// already in the history.
func (s *Session) ContinueStream(ctx context.Context, onChunk func(StreamChunk)) (*ChatResponse, error) {
	s.logMessage("Continue", "(resuming from existing state)")

	req := &ChatRequest{
		Model:         s.model,
		Messages:      s.buildCurrentMessages(),
		StopSequences: s.stopSequences,
		Tools:         s.tools,
		PromptCaching:       s.promptCaching,
		ConversationCaching: s.conversationCaching,
	}

	sr, err := s.streamWithRetry(ctx, req, onChunk)
	if err != nil {
		return nil, err
	}

	content := s.stripStopSequences(sr.TextContent)

	s.logMessage("LLM Response", content)

	// Build the final response
	resp := &ChatResponse{
		ID:            uuid.New().String(),
		Content:       content,
		ContentBlocks: sr.ContentBlocks,
		FinishReason:  sr.StopReason,
	}

	// Capture usage from the final chunk if provider included it
	if sr.LastChunk.Usage != nil {
		resp.Usage = *sr.LastChunk.Usage
	}

	// Append ONLY the assistant response (no user message — it's already in history)
	s.messages = append(s.messages, s.buildAssistantMessage(content, sr.ContentBlocks))

	return resp, nil
}

// buildCurrentMessages builds system prompts + existing history, no new user message.
func (s *Session) buildCurrentMessages() []Message {
	var msgs []Message
	for _, sp := range s.systemPrompts {
		msgs = append(msgs, Message{Role: RoleSystem, Content: sp})
	}
	msgs = append(msgs, s.messages...)
	return msgs
}

// CompactSettings contains settings for context compaction
type CompactSettings struct {
	TokenLimit    int // Trigger compaction when input tokens exceed this
	TurnRetention int // Keep this many recent turns uncompacted
}

// MessageStatsResult holds aggregate statistics about session messages.
type MessageStatsResult struct {
	UserCount      int
	AssistantCount int
	SystemCount    int
	PayloadBytes   int
}

// MessageStats computes message counts by role and total byte size of the conversation.
func (s *Session) MessageStats() MessageStatsResult {
	var r MessageStatsResult
	for _, m := range s.messages {
		switch m.Role {
		case RoleUser:
			r.UserCount++
		case RoleAssistant:
			r.AssistantCount++
		case RoleSystem:
			r.SystemCount++
		}
		r.PayloadBytes += len(m.GetTextContent())
	}
	for _, sp := range s.systemPrompts {
		r.SystemCount++
		r.PayloadBytes += len(sp)
	}
	return r
}

// MessageCount returns the number of conversation messages (excluding system prompts).
func (s *Session) MessageCount() int {
	return len(s.messages)
}

// DropOldMessages drops the oldest messages, keeping only the last `keep` messages.
// System prompts are never affected (they are stored separately).
// Returns the number of messages dropped.
func (s *Session) DropOldMessages(keep int) int {
	if len(s.messages) <= keep {
		return 0
	}
	dropCount := len(s.messages) - keep
	s.messages = s.messages[dropCount:]
	return dropCount
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

// CompactWithContext is like Compact but injects additional context into the compaction summary.
// Used by commanders to preserve state like dataset progress and subtask status.
func (s *Session) CompactWithContext(turnRetention int, extraContext string) int {
	// A turn is a user message + assistant response pair (2 messages)
	messagesToRetain := turnRetention * 2

	if len(s.messages) <= messagesToRetain {
		return 0
	}

	compactEnd := len(s.messages) - messagesToRetain
	if compactEnd <= 0 {
		return 0
	}

	summary := s.buildCompactionSummary(s.messages[:compactEnd])

	// Inject extra context after the opening tag and description
	if extraContext != "" {
		insertPoint := strings.Index(summary, "\n\n")
		if insertPoint != -1 {
			summary = summary[:insertPoint+2] + extraContext + "\n" + summary[insertPoint+2:]
		}
	}

	newMessages := make([]Message, 0, 1+messagesToRetain)
	newMessages = append(newMessages, Message{
		Role:    RoleUser,
		Content: summary,
	})
	newMessages = append(newMessages, s.messages[compactEnd:]...)

	compactedCount := compactEnd
	s.messages = newMessages

	s.logMessage("Compaction", fmt.Sprintf("Compacted %d messages into summary with extra context. Retained last %d turns.", compactedCount, turnRetention))

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

		// Skip tool results
		if msg.HasParts() {
			isToolResult := false
			for _, part := range msg.Parts {
				if part.Type == ContentTypeToolResult {
					isToolResult = true
					break
				}
			}
			if isToolResult {
				continue
			}
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

		// Extract tool calls from assistant messages with native tool_use blocks
		if msg.Role == RoleAssistant && msg.HasParts() {
			for _, part := range msg.Parts {
				if part.Type == ContentTypeToolUse && part.ToolUse != nil {
					toolCalls = append(toolCalls, part.ToolUse.Name)
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
		Tools:         s.tools,
		PromptCaching:       s.promptCaching,
		ConversationCaching: s.conversationCaching,
	}

	sr, err := s.streamWithRetry(ctx, req, onChunk)
	if err != nil {
		return nil, err
	}

	content := s.stripStopSequences(sr.TextContent)

	s.logMessage("LLM Response", content)

	// Build the final response
	resp := &ChatResponse{
		ID:            uuid.New().String(),
		Content:       content,
		ContentBlocks: sr.ContentBlocks,
		FinishReason:  sr.StopReason,
	}

	// Capture usage from the final chunk if provider included it
	if sr.LastChunk.Usage != nil {
		resp.Usage = *sr.LastChunk.Usage
	}

	// Append user message and assistant response to history
	s.messages = append(s.messages, userMsg)
	s.messages = append(s.messages, s.buildAssistantMessage(content, sr.ContentBlocks))

	return resp, nil
}

// streamResult holds the accumulated result of reading a stream
type streamResult struct {
	TextContent   string
	ContentBlocks []ContentBlock
	StopReason    string
	LastChunk     StreamChunk
}

// readStream reads chunks from a stream channel, respecting context cancellation.
// Accumulates both text content and structured content blocks (including tool calls).
func readStream(ctx context.Context, stream <-chan StreamChunk, onChunk func(StreamChunk)) (streamResult, error) {
	var result streamResult
	var contentBuilder strings.Builder

	// Track tool calls being accumulated from stream deltas
	var currentToolID string
	var currentToolName string
	var currentToolInput strings.Builder

	for {
		select {
		case <-ctx.Done():
			result.TextContent = contentBuilder.String()
			return result, ctx.Err()
		case chunk, ok := <-stream:
			if !ok {
				result.TextContent = contentBuilder.String()
				return result, nil
			}
			if chunk.Error != nil {
				result.TextContent = contentBuilder.String()
				return result, chunk.Error
			}

			// Accumulate text content
			if chunk.Content != "" {
				contentBuilder.WriteString(chunk.Content)
			}

			// Track tool call streaming
			if chunk.ToolCallStart != nil {
				currentToolID = chunk.ToolCallStart.ID
				currentToolName = chunk.ToolCallStart.Name
				currentToolInput.Reset()
			}
			if chunk.ToolCallDelta != "" {
				currentToolInput.WriteString(chunk.ToolCallDelta)
			}
			if chunk.ToolCallDone != nil {
				// Finalize the tool call block
				result.ContentBlocks = append(result.ContentBlocks, ContentBlock{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:    currentToolID,
						Name:  currentToolName,
						Input: json.RawMessage(currentToolInput.String()),
					},
				})
				currentToolID = ""
				currentToolName = ""
				currentToolInput.Reset()
			}

			if onChunk != nil {
				onChunk(chunk)
			}

			if chunk.Done {
				result.StopReason = chunk.StopReason
				// Use provider-accumulated ContentBlocks if available, otherwise use ours
				if len(chunk.ContentBlocks) > 0 {
					result.ContentBlocks = chunk.ContentBlocks
				}
			}

			result.LastChunk = chunk
		}
	}
}
