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
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		}
	}

	return msgs, systemPrompts
}
