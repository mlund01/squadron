package llm

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

type AnthropicProvider struct {
	client *anthropic.Client
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{client: &client}
}

func (p *AnthropicProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	msgs, systemPrompts := p.convertMessages(req.Messages, req.PromptCaching, req.ConversationCaching)

	maxTokens := int64(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTokens,
		Messages:  msgs,
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
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTokens,
		Messages:  msgs,
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
		var currentToolInput string

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockStartEvent:
				switch e.ContentBlock.Type {
				case "tool_use":
					currentToolID = e.ContentBlock.ID
					currentToolName = e.ContentBlock.Name
					currentToolInput = ""
					chunks <- StreamChunk{
						ToolCallStart: &ToolCallStartChunk{
							ID:   currentToolID,
							Name: currentToolName,
						},
					}
				case "text":
					// Text block starting — nothing to emit yet
				}

			case anthropic.ContentBlockDeltaEvent:
				switch e.Delta.Type {
				case "text_delta":
					chunks <- StreamChunk{
						Content: e.Delta.Text,
					}
				case "input_json_delta":
					currentToolInput += e.Delta.PartialJSON
					chunks <- StreamChunk{
						ToolCallDelta: e.Delta.PartialJSON,
					}
				}

			case anthropic.ContentBlockStopEvent:
				// If we were accumulating a tool call, finalize it
				if currentToolID != "" {
					contentBlocks = append(contentBlocks, ContentBlock{
						Type: ContentTypeToolUse,
						ToolUse: &ToolUseBlock{
							ID:    currentToolID,
							Name:  currentToolName,
							Input: json.RawMessage(currentToolInput),
						},
					})
					id := currentToolID
					chunks <- StreamChunk{
						ToolCallDone: &id,
					}
					currentToolID = ""
					currentToolName = ""
					currentToolInput = ""
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
	// If no Parts, use simple text content
	if !m.HasParts() {
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
		}
	}

	return blocks
}
