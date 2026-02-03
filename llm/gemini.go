package llm

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type GeminiProvider struct {
	client *genai.Client
}

func NewGeminiProvider(ctx context.Context, apiKey string) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}
	return &GeminiProvider{client: client}, nil
}

func (p *GeminiProvider) Close() error {
	return p.client.Close()
}

func (p *GeminiProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	model := p.client.GenerativeModel(req.Model)

	// Set system instructions
	systemContent := p.extractSystemPrompts(req.Messages)
	if systemContent != "" {
		model.SystemInstruction = genai.NewUserContent(genai.Text(systemContent))
	}

	// Start chat and set history
	chat := model.StartChat()
	chat.History = p.convertHistory(req.Messages)

	// Get the last user message parts
	lastUserParts := p.getLastUserMessageParts(req.Messages)

	resp, err := chat.SendMessage(ctx, lastUserParts...)
	if err != nil {
		return nil, err
	}

	content := p.extractContent(resp)

	return &ChatResponse{
		ID:           uuid.New().String(),
		Content:      content,
		FinishReason: string(resp.Candidates[0].FinishReason),
		Usage: Usage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		},
	}, nil
}

func (p *GeminiProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	model := p.client.GenerativeModel(req.Model)

	// Set system instructions
	systemContent := p.extractSystemPrompts(req.Messages)
	if systemContent != "" {
		model.SystemInstruction = genai.NewUserContent(genai.Text(systemContent))
	}

	// Start chat and set history
	chat := model.StartChat()
	chat.History = p.convertHistory(req.Messages)

	// Get the last user message parts
	lastUserParts := p.getLastUserMessageParts(req.Messages)

	iter := chat.SendMessageStream(ctx, lastUserParts...)

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		for {
			resp, err := iter.Next()
			if err == iterator.Done {
				chunks <- StreamChunk{Done: true}
				break
			}
			if err != nil {
				chunks <- StreamChunk{Error: err, Done: true}
				break
			}

			content := p.extractContent(resp)
			if content != "" {
				chunks <- StreamChunk{
					Content: content,
					Done:    false,
				}
			}
		}
	}()

	return chunks, nil
}

func (p *GeminiProvider) extractSystemPrompts(messages []Message) string {
	var system string
	for _, m := range messages {
		if m.Role == RoleSystem {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		}
	}
	return system
}

func (p *GeminiProvider) convertHistory(messages []Message) []*genai.Content {
	var history []*genai.Content

	// Filter out system messages and the last user message
	nonSystemMsgs := make([]Message, 0)
	for _, m := range messages {
		if m.Role != RoleSystem {
			nonSystemMsgs = append(nonSystemMsgs, m)
		}
	}

	// Exclude the last user message (it's sent separately)
	if len(nonSystemMsgs) > 0 {
		nonSystemMsgs = nonSystemMsgs[:len(nonSystemMsgs)-1]
	}

	for _, m := range nonSystemMsgs {
		var role string
		switch m.Role {
		case RoleUser:
			role = "user"
		case RoleAssistant:
			role = "model"
		default:
			continue
		}

		history = append(history, &genai.Content{
			Role:  role,
			Parts: p.buildGeminiParts(m),
		})
	}

	return history
}

// buildGeminiParts converts a Message to Gemini parts
func (p *GeminiProvider) buildGeminiParts(m Message) []genai.Part {
	if !m.HasParts() {
		return []genai.Part{genai.Text(m.Content)}
	}

	var parts []genai.Part
	for _, part := range m.Parts {
		switch part.Type {
		case ContentTypeText:
			parts = append(parts, genai.Text(part.Text))
		case ContentTypeImage:
			if part.ImageData != nil {
				// Decode base64 to raw bytes for Gemini
				data, err := base64.StdEncoding.DecodeString(part.ImageData.Data)
				if err == nil {
					parts = append(parts, genai.ImageData(part.ImageData.MediaType, data))
				}
			}
		}
	}

	return parts
}

// getLastUserMessageParts returns the last user message as Gemini parts
func (p *GeminiProvider) getLastUserMessageParts(messages []Message) []genai.Part {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			return p.buildGeminiParts(messages[i])
		}
	}
	return []genai.Part{genai.Text("")}
}

func (p *GeminiProvider) extractContent(resp *genai.GenerateContentResponse) string {
	var content string
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				content += fmt.Sprintf("%v", part)
			}
		}
	}
	return content
}
