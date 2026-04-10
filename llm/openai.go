package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
)

// modelSupportsStop returns false for models that reject the 'stop' parameter
// (reasoning models like o-series and GPT-5 family).
func modelSupportsStop(model string) bool {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return false
	case strings.HasPrefix(m, "gpt-5"):
		return false
	default:
		return true
	}
}

type OpenAIProvider struct {
	client *openai.Client
}

func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIProvider{client: &client}
}

// NewOpenAICompatibleProvider creates a provider that targets an OpenAI-compatible
// API at the given base URL (e.g. Ollama at http://localhost:11434/v1).
func NewOpenAICompatibleProvider(baseURL string) *OpenAIProvider {
	client := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey("ollama"), // dummy key; local servers ignore it
	)
	return &OpenAIProvider{client: &client}
}

func (p *OpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	msgs := p.convertMessages(req.Messages)

	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
	}

	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}

	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}

	if len(req.StopSequences) > 0 && modelSupportsStop(req.Model) {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: req.StopSequences,
		}
	}

	if len(req.Tools) > 0 {
		params.Tools = p.convertTools(req.Tools)
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	var content string
	var contentBlocks []ContentBlock
	if len(resp.Choices) > 0 {
		msg := resp.Choices[0].Message
		content = msg.Content

		// Add text content block if present
		if content != "" {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeText,
				Text: content,
			})
		}

		// Add tool call content blocks
		for _, tc := range msg.ToolCalls {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type: ContentTypeToolUse,
				ToolUse: &ToolUseBlock{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				},
			})
		}
	}

	usage := Usage{
		InputTokens:  int(resp.Usage.PromptTokens),
		OutputTokens: int(resp.Usage.CompletionTokens),
	}
	// OpenAI includes cached tokens within prompt_tokens (subset), but our Usage
	// convention treats cache tokens as separate (like Anthropic). Normalize by
	// subtracting cached from input so totals are consistent across providers.
	if resp.Usage.PromptTokensDetails.CachedTokens > 0 {
		usage.CacheReadTokens = int(resp.Usage.PromptTokensDetails.CachedTokens)
		usage.InputTokens -= usage.CacheReadTokens
	}

	var finishReason string
	if len(resp.Choices) > 0 {
		finishReason = string(resp.Choices[0].FinishReason)
	}

	return &ChatResponse{
		ID:            resp.ID,
		Content:       content,
		ContentBlocks: contentBlocks,
		FinishReason:  finishReason,
		Usage:         usage,
	}, nil
}

func (p *OpenAIProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	msgs := p.convertMessages(req.Messages)

	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
		// Enable usage reporting in streaming responses
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}

	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}

	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}

	if len(req.StopSequences) > 0 && modelSupportsStop(req.Model) {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfStringArray: req.StopSequences,
		}
	}

	if len(req.Tools) > 0 {
		params.Tools = p.convertTools(req.Tools)
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		var finalUsage Usage
		var stopReason string
		var contentBlocks []ContentBlock

		// Track in-progress tool calls by index
		type toolCallState struct {
			id        string
			name      string
			arguments string
		}
		toolCalls := make(map[int]*toolCallState)

		for stream.Next() {
			chunk := stream.Current()

			// Capture usage from final chunk (when include_usage is enabled)
			if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
				finalUsage.InputTokens = int(chunk.Usage.PromptTokens)
				finalUsage.OutputTokens = int(chunk.Usage.CompletionTokens)
				if chunk.Usage.PromptTokensDetails.CachedTokens > 0 {
					finalUsage.CacheReadTokens = int(chunk.Usage.PromptTokensDetails.CachedTokens)
					finalUsage.InputTokens -= finalUsage.CacheReadTokens
				}
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]
				delta := choice.Delta

				// Stream text content
				if delta.Content != "" {
					chunks <- StreamChunk{
						Content: delta.Content,
					}
				}

				// Stream tool calls
				for _, tc := range delta.ToolCalls {
					idx := int(tc.Index)
					state, exists := toolCalls[idx]
					if !exists {
						state = &toolCallState{}
						toolCalls[idx] = state
					}

					if tc.ID != "" {
						state.id = tc.ID
					}
					if tc.Function.Name != "" {
						state.name = tc.Function.Name
						// Emit tool call start
						chunks <- StreamChunk{
							ToolCallStart: &ToolCallStartChunk{
								ID:   state.id,
								Name: state.name,
							},
						}
					}
					if tc.Function.Arguments != "" {
						state.arguments += tc.Function.Arguments
						chunks <- StreamChunk{
							ToolCallDelta: tc.Function.Arguments,
						}
					}
				}

				if choice.FinishReason != "" {
					stopReason = string(choice.FinishReason)

					// Finalize any tool calls
					for _, state := range toolCalls {
						if state.id != "" {
							contentBlocks = append(contentBlocks, ContentBlock{
								Type: ContentTypeToolUse,
								ToolUse: &ToolUseBlock{
									ID:    state.id,
									Name:  state.name,
									Input: json.RawMessage(state.arguments),
								},
							})
							id := state.id
							chunks <- StreamChunk{
								ToolCallDone: &id,
							}
						}
					}

					chunks <- StreamChunk{
						Done:          true,
						Usage:         &finalUsage,
						StopReason:    stopReason,
						ContentBlocks: contentBlocks,
					}
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

// convertTools converts provider-agnostic ToolDefinitions to OpenAI tool params
func (p *OpenAIProvider) convertTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	result := make([]openai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		// Parse the JSON schema into FunctionParameters (map[string]any)
		var params shared.FunctionParameters
		if err := json.Unmarshal(t.InputSchema, &params); err != nil {
			continue
		}

		result = append(result, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: param.NewOpt(t.Description),
				Parameters:  params,
			},
		})
	}
	return result
}

func (p *OpenAIProvider) convertMessages(messages []Message) []openai.ChatCompletionMessageParamUnion {
	var msgs []openai.ChatCompletionMessageParamUnion

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case RoleUser:
			msgs = append(msgs, p.buildUserMessage(m))
		case RoleAssistant:
			msgs = append(msgs, p.buildAssistantMessage(m))
		}
	}

	return msgs
}

// buildUserMessage creates an OpenAI user message, handling multimodal content and tool results
func (p *OpenAIProvider) buildUserMessage(m Message) openai.ChatCompletionMessageParamUnion {
	// Check if this message contains tool results — these need separate tool messages
	if m.HasParts() {
		// Check if ALL parts are tool results
		allToolResults := true
		for _, part := range m.Parts {
			if part.Type != ContentTypeToolResult {
				allToolResults = false
				break
			}
		}

		// If it's a pure tool result message, return the first one
		// (OpenAI tool messages are one per tool call, so caller should handle multiple)
		if allToolResults && len(m.Parts) > 0 {
			tr := m.Parts[0].ToolResult
			return openai.ToolMessage(tr.Content, tr.ToolUseID)
		}
	}

	// If no Parts, use simple text content
	if !m.HasParts() {
		return openai.UserMessage(m.Content)
	}

	// Build content parts from Parts
	var parts []openai.ChatCompletionContentPartUnionParam
	for _, part := range m.Parts {
		switch part.Type {
		case ContentTypeText:
			parts = append(parts, openai.TextContentPart(part.Text))
		case ContentTypeImage:
			if part.ImageData != nil {
				// OpenAI expects data URLs for base64 images
				dataURL := fmt.Sprintf("data:%s;base64,%s", part.ImageData.MediaType, part.ImageData.Data)
				parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL: dataURL,
				}))
			}
		}
	}

	return openai.UserMessage(parts)
}

// buildAssistantMessage creates an OpenAI assistant message, including tool calls if present
func (p *OpenAIProvider) buildAssistantMessage(m Message) openai.ChatCompletionMessageParamUnion {
	if !m.HasParts() {
		return openai.AssistantMessage(m.GetTextContent())
	}

	// Check if the message has tool use blocks
	var toolCalls []openai.ChatCompletionMessageToolCallParam
	var textContent string
	for _, part := range m.Parts {
		switch part.Type {
		case ContentTypeText:
			textContent += part.Text
		case ContentTypeToolUse:
			if part.ToolUse != nil {
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
					ID: part.ToolUse.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      part.ToolUse.Name,
						Arguments: string(part.ToolUse.Input),
					},
				})
			}
		}
	}

	if len(toolCalls) > 0 {
		// Assistant message with tool calls
		var assistant openai.ChatCompletionAssistantMessageParam
		if textContent != "" {
			assistant.Content.OfString = param.NewOpt(textContent)
		}
		assistant.ToolCalls = toolCalls
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
	}

	return openai.AssistantMessage(textContent)
}
