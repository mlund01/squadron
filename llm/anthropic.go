package llm

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

type AnthropicProvider struct {
	client *anthropic.Client
}

func NewAnthropicProvider(apiKey, baseURL string) *AnthropicProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := anthropic.NewClient(opts...)
	return &AnthropicProvider{client: &client}
}

func (p *AnthropicProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	msgs, systemPrompts := p.convertMessages(req.Messages, req.PromptCaching, req.ConversationCaching)

	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 8192
	}

	// Extended thinking: budget_tokens must be < max_tokens, so clamp upward
	// when needed. Anthropic also requires temperature=1 and rejects
	// top_p/top_k when thinking is on; we don't set those today, but any
	// future code that does must skip them when params.Thinking is set.
	var thinkingBudget int64
	if req.Reasoning != "" {
		thinkingBudget = anthropicBudgetTokens(req.Reasoning)
		if thinkingBudget > 0 && maxTokens < thinkingBudget+8192 {
			maxTokens = thinkingBudget + 8192
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTokens,
		Messages:  msgs,
	}

	if thinkingBudget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(thinkingBudget)
	}

	if len(systemPrompts) > 0 {
		params.System = systemPrompts
	}

	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	if len(req.Tools) > 0 {
		params.Tools = p.convertTools(req.Tools)
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	var content string
	var contentBlocks []ContentBlock
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content += block.Text
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeText,
				Text: block.Text,
			})
		case "tool_use":
			tb := block.AsToolUse()
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeToolUse,
				ToolUse: &ToolUseBlock{
					ID:    tb.ID,
					Name:  tb.Name,
					Input: tb.Input,
				},
			})
		case "thinking":
			tb := block.AsThinking()
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeThinking,
				Thinking: &ThinkingBlock{
					Text:      tb.Thinking,
					Signature: tb.Signature,
				},
			})
		case "redacted_thinking":
			rb := block.AsRedactedThinking()
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeThinking,
				Thinking: &ThinkingBlock{
					RedactedData: rb.Data,
				},
			})
		}
	}

	return &ChatResponse{
		ID:            resp.ID,
		Content:       content,
		ContentBlocks: contentBlocks,
		FinishReason:  string(resp.StopReason),
		Usage: Usage{
			InputTokens:      int(resp.Usage.InputTokens),
			OutputTokens:     int(resp.Usage.OutputTokens),
			CacheWriteTokens: int(resp.Usage.CacheCreationInputTokens),
			CacheReadTokens:  int(resp.Usage.CacheReadInputTokens),
		},
	}, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	msgs, systemPrompts := p.convertMessages(req.Messages, req.PromptCaching, req.ConversationCaching)

	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 8192
	}

	var thinkingBudget int64
	if req.Reasoning != "" {
		thinkingBudget = anthropicBudgetTokens(req.Reasoning)
		if thinkingBudget > 0 && maxTokens < thinkingBudget+8192 {
			maxTokens = thinkingBudget + 8192
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTokens,
		Messages:  msgs,
	}

	if thinkingBudget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(thinkingBudget)
	}

	if len(systemPrompts) > 0 {
		params.System = systemPrompts
	}

	if len(req.StopSequences) > 0 {
		params.StopSequences = req.StopSequences
	}

	if len(req.Tools) > 0 {
		params.Tools = p.convertTools(req.Tools)
	}

	stream := p.client.Messages.NewStreaming(ctx, params)

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		var finalUsage Usage
		var stopReason string
		var contentBlocks []ContentBlock
		// Track the current tool call being streamed
		var currentToolID string
		var currentToolName string
		var currentToolInput strings.Builder
		// Track the current thinking block being streamed. Anthropic sends
		// thinking content via thinking_delta and the cryptographic signature
		// via signature_delta — both within the same content block, so we
		// accumulate both until ContentBlockStopEvent finalizes them.
		var currentThinkingText strings.Builder
		var currentThinkingSignature strings.Builder
		var currentThinkingRedacted string
		var inThinkingBlock bool

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				switch e.ContentBlock.Type {
				case "tool_use":
					currentToolID = e.ContentBlock.ID
					currentToolName = e.ContentBlock.Name
					currentToolInput.Reset()
					chunks <- StreamChunk{
						ToolCallStart: &ToolCallStartChunk{
							ID:   currentToolID,
							Name: currentToolName,
						},
					}
				case "text":
				case "thinking":
					inThinkingBlock = true
					currentThinkingText.Reset()
					currentThinkingSignature.Reset()
					currentThinkingRedacted = ""
					if e.ContentBlock.Thinking != "" {
						currentThinkingText.WriteString(e.ContentBlock.Thinking)
					}
					if e.ContentBlock.Signature != "" {
						currentThinkingSignature.WriteString(e.ContentBlock.Signature)
					}
					chunks <- StreamChunk{ReasoningStart: true}
					if e.ContentBlock.Thinking != "" {
						chunks <- StreamChunk{ReasoningDelta: e.ContentBlock.Thinking}
					}
				case "redacted_thinking":
					// Redacted thinking has no surfaceable text — round-trip
					// the encrypted data without emitting reasoning events.
					inThinkingBlock = true
					currentThinkingText.Reset()
					currentThinkingSignature.Reset()
					currentThinkingRedacted = e.ContentBlock.Data
				}

			case anthropic.ContentBlockDeltaEvent:
				switch e.Delta.Type {
				case "text_delta":
					chunks <- StreamChunk{
						Content: e.Delta.Text,
					}
				case "input_json_delta":
					currentToolInput.WriteString(e.Delta.PartialJSON)
					chunks <- StreamChunk{
						ToolCallDelta: e.Delta.PartialJSON,
					}
				case "thinking_delta":
					currentThinkingText.WriteString(e.Delta.Thinking)
					chunks <- StreamChunk{ReasoningDelta: e.Delta.Thinking}
				case "signature_delta":
					currentThinkingSignature.WriteString(e.Delta.Signature)
				}

			case anthropic.ContentBlockStopEvent:
				if currentToolID != "" {
					contentBlocks = append(contentBlocks, ContentBlock{
						Type: ContentTypeToolUse,
						ToolUse: &ToolUseBlock{
							ID:    currentToolID,
							Name:  currentToolName,
							Input: json.RawMessage(currentToolInput.String()),
						},
					})
					id := currentToolID
					chunks <- StreamChunk{
						ToolCallDone: &id,
					}
					currentToolID = ""
					currentToolName = ""
					currentToolInput.Reset()
				}
				if inThinkingBlock {
					contentBlocks = append(contentBlocks, ContentBlock{
						Type: ContentTypeThinking,
						Thinking: &ThinkingBlock{
							Text:         currentThinkingText.String(),
							Signature:    currentThinkingSignature.String(),
							RedactedData: currentThinkingRedacted,
						},
					})
					// Redacted thinking never opened a reasoning window —
					// don't emit a Done event for it either.
					if currentThinkingRedacted == "" {
						chunks <- StreamChunk{ReasoningDone: true}
					}
					inThinkingBlock = false
					currentThinkingText.Reset()
					currentThinkingSignature.Reset()
					currentThinkingRedacted = ""
				}

			case anthropic.MessageDeltaEvent:
				finalUsage.OutputTokens = int(e.Usage.OutputTokens)
				stopReason = string(e.Delta.StopReason)

			case anthropic.MessageStartEvent:
				finalUsage.InputTokens = int(e.Message.Usage.InputTokens)
				finalUsage.CacheWriteTokens = int(e.Message.Usage.CacheCreationInputTokens)
				finalUsage.CacheReadTokens = int(e.Message.Usage.CacheReadInputTokens)

			case anthropic.MessageStopEvent:
				// Collect any text content that was streamed
				textContent := stream.Current()
				_ = textContent // text was already sent via chunks

				chunks <- StreamChunk{
					Done:          true,
					Usage:         &finalUsage,
					StopReason:    stopReason,
					ContentBlocks: contentBlocks,
				}
			}
		}

		if err := stream.Err(); err != nil {
			chunks <- StreamChunk{
				Error: err,
				Done:  true,
			}
		}
	}()

	return chunks, nil
}

// convertTools converts provider-agnostic ToolDefinitions to Anthropic tool params
func (p *AnthropicProvider) convertTools(tools []ToolDefinition) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		// Parse the JSON schema into the Anthropic ToolInputSchemaParam structure
		var schemaMap map[string]any
		if err := json.Unmarshal(t.InputSchema, &schemaMap); err != nil {
			continue
		}

		inputSchema := anthropic.ToolInputSchemaParam{
			Properties: schemaMap["properties"],
		}
		if req, ok := schemaMap["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					inputSchema.Required = append(inputSchema.Required, s)
				}
			}
		}

		result = append(result, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: param.NewOpt(t.Description),
				InputSchema: inputSchema,
			},
		})
	}
	return result
}

func (p *AnthropicProvider) convertMessages(messages []Message, promptCaching bool, conversationCaching bool) ([]anthropic.MessageParam, []anthropic.TextBlockParam) {
	var msgs []anthropic.MessageParam
	var systemPrompts []anthropic.TextBlockParam

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			systemPrompts = append(systemPrompts, anthropic.TextBlockParam{
				Type: "text",
				Text: m.Content,
			})
		case RoleUser:
			contentBlocks := p.buildContentBlocks(m)
			msgs = append(msgs, anthropic.NewUserMessage(contentBlocks...))
		case RoleAssistant:
			contentBlocks := p.buildContentBlocks(m)
			msgs = append(msgs, anthropic.NewAssistantMessage(contentBlocks...))
		}
	}

	if promptCaching {
		// Set cache_control on the last system prompt
		if len(systemPrompts) > 0 {
			systemPrompts[len(systemPrompts)-1].CacheControl = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
		}
	}

	if conversationCaching {
		// Set cache_control on the last content block of the last user message
		// so that conversation history up to this point is cached across turns.
		// Disabled when pruning is active since the shifting window invalidates cache every turn.
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == anthropic.MessageParamRoleUser && len(msgs[i].Content) > 0 {
				lastBlock := &msgs[i].Content[len(msgs[i].Content)-1]
				if cc := lastBlock.GetCacheControl(); cc != nil {
					*cc = anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
				}
				break
			}
		}
	}

	return msgs, systemPrompts
}

// buildContentBlocks converts a Message to Anthropic content blocks
func (p *AnthropicProvider) buildContentBlocks(m Message) []anthropic.ContentBlockParamUnion {
	// If no Parts, use simple text content (skip empty blocks)
	if !m.HasParts() {
		if m.Content == "" {
			return []anthropic.ContentBlockParamUnion{}
		}
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)}
	}

	// Build content blocks from Parts
	var blocks []anthropic.ContentBlockParamUnion
	for _, part := range m.Parts {
		switch part.Type {
		case ContentTypeText:
			blocks = append(blocks, anthropic.NewTextBlock(part.Text))
		case ContentTypeImage:
			if part.ImageData != nil {
				blocks = append(blocks, anthropic.NewImageBlockBase64(
					part.ImageData.MediaType,
					part.ImageData.Data,
				))
			}
		case ContentTypeToolUse:
			if part.ToolUse != nil {
				// Convert json.RawMessage input to any for the SDK
				var input any
				if err := json.Unmarshal(part.ToolUse.Input, &input); err != nil {
					input = map[string]any{}
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(part.ToolUse.ID, input, part.ToolUse.Name))
			}
		case ContentTypeToolResult:
			if part.ToolResult != nil {
				blocks = append(blocks, anthropic.NewToolResultBlock(part.ToolResult.ToolUseID, part.ToolResult.Content, part.ToolResult.IsError))
			}
		case ContentTypeThinking:
			// Anthropic requires thinking blocks to be echoed back on
			// subsequent turns when extended thinking is enabled and the
			// previous turn used tools — multi-turn requests fail otherwise.
			// Session.buildAssistantMessage places thinking parts first; the
			// loop here preserves that order.
			if part.Thinking != nil {
				if part.Thinking.RedactedData != "" {
					blocks = append(blocks, anthropic.NewRedactedThinkingBlock(part.Thinking.RedactedData))
				} else if part.Thinking.Signature != "" {
					// Only echo when we have the signature (which is required
					// by Anthropic). Reasoning captured from a different
					// provider has no signature and gets dropped silently.
					blocks = append(blocks, anthropic.NewThinkingBlock(part.Thinking.Signature, part.Thinking.Text))
				}
			}
		case ContentTypeProviderRaw:
			// Provider-specific block from a prior turn. Only echo back when
			// it originated from this same provider; otherwise drop silently
			// so a session that switched providers stays valid.
			_ = part
		}
	}

	return blocks
}
