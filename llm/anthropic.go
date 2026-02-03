package llm

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type AnthropicProvider struct {
	client *anthropic.Client
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{client: &client}
}

func (p *AnthropicProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	msgs, systemPrompts := p.convertMessages(req.Messages)

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

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	var content string
	for _, block := range resp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return &ChatResponse{
		ID:           resp.ID,
		Content:      content,
		FinishReason: string(resp.StopReason),
		Usage: Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	msgs, systemPrompts := p.convertMessages(req.Messages)

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

	stream := p.client.Messages.NewStreaming(ctx, params)

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		for stream.Next() {
			event := stream.Current()

			switch e := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				if e.Delta.Type == "text_delta" {
					chunks <- StreamChunk{
						Content: e.Delta.Text,
						Done:    false,
					}
				}
			case anthropic.MessageStopEvent:
				chunks <- StreamChunk{
					Done: true,
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

func (p *AnthropicProvider) convertMessages(messages []Message) ([]anthropic.MessageParam, []anthropic.TextBlockParam) {
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
		}
	}

	return blocks
}
