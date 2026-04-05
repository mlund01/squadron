package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	var content string
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
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

	return &ChatResponse{
		ID:           resp.ID,
		Content:      content,
		FinishReason: string(resp.Choices[0].FinishReason),
		Usage:        usage,
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

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		var finalUsage Usage

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
				delta := chunk.Choices[0].Delta
				if delta.Content != "" {
					chunks <- StreamChunk{
						Content: delta.Content,
						Done:    false,
					}
				}

				if chunk.Choices[0].FinishReason != "" {
					chunks <- StreamChunk{
						Done:  true,
						Usage: &finalUsage,
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

func (p *OpenAIProvider) convertMessages(messages []Message) []openai.ChatCompletionMessageParamUnion {
	var msgs []openai.ChatCompletionMessageParamUnion

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case RoleUser:
			msgs = append(msgs, p.buildUserMessage(m))
		case RoleAssistant:
			msgs = append(msgs, openai.AssistantMessage(m.GetTextContent()))
		}
	}

	return msgs
}

// buildUserMessage creates an OpenAI user message, handling multimodal content
func (p *OpenAIProvider) buildUserMessage(m Message) openai.ChatCompletionMessageParamUnion {
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
