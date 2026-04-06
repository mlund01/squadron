package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

	// Set tools if provided
	if len(req.Tools) > 0 {
		model.Tools = p.convertTools(req.Tools)
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

	content, contentBlocks := p.extractContentAndBlocks(resp)

	return &ChatResponse{
		ID:            uuid.New().String(),
		Content:       content,
		ContentBlocks: contentBlocks,
		FinishReason:  string(resp.Candidates[0].FinishReason),
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

	// Set tools if provided
	if len(req.Tools) > 0 {
		model.Tools = p.convertTools(req.Tools)
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

		var finalUsage Usage
		var allContentBlocks []ContentBlock

		for {
			resp, err := iter.Next()
			if err == iterator.Done {
				chunks <- StreamChunk{
					Done:          true,
					Usage:         &finalUsage,
					ContentBlocks: allContentBlocks,
				}
				break
			}
			if err != nil {
				chunks <- StreamChunk{Error: err, Done: true}
				break
			}

			// Capture usage from each response (accumulates over stream)
			if resp.UsageMetadata != nil {
				finalUsage.InputTokens = int(resp.UsageMetadata.PromptTokenCount)
				finalUsage.OutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
			}

			// Extract content and function calls from this chunk
			for _, cand := range resp.Candidates {
				if cand.Content == nil {
					continue
				}
				for _, part := range cand.Content.Parts {
					switch v := part.(type) {
					case genai.Text:
						text := string(v)
						if text != "" {
							chunks <- StreamChunk{Content: text}
						}
					case genai.FunctionCall:
						// Generate an ID for the function call (Gemini doesn't provide one)
						callID := uuid.New().String()
						inputBytes, _ := json.Marshal(v.Args)

						chunks <- StreamChunk{
							ToolCallStart: &ToolCallStartChunk{
								ID:   callID,
								Name: v.Name,
							},
						}
						chunks <- StreamChunk{
							ToolCallDelta: string(inputBytes),
						}

						allContentBlocks = append(allContentBlocks, ContentBlock{
							Type: ContentTypeToolUse,
							ToolUse: &ToolUseBlock{
								ID:    callID,
								Name:  v.Name,
								Input: inputBytes,
							},
						})

						chunks <- StreamChunk{
							ToolCallDone: &callID,
						}
					}
				}
			}
		}
	}()

	return chunks, nil
}

// convertTools converts provider-agnostic ToolDefinitions to Gemini tools
func (p *GeminiProvider) convertTools(tools []ToolDefinition) []*genai.Tool {
	var funcDecls []*genai.FunctionDeclaration
	for _, t := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
		}

		// Convert JSON Schema to Gemini Schema
		var schemaMap map[string]any
		if err := json.Unmarshal(t.InputSchema, &schemaMap); err == nil {
			fd.Parameters = jsonSchemaToGeminiSchema(schemaMap)
		}

		funcDecls = append(funcDecls, fd)
	}
	return []*genai.Tool{{FunctionDeclarations: funcDecls}}
}

// jsonSchemaToGeminiSchema converts a JSON Schema map to a Gemini Schema
func jsonSchemaToGeminiSchema(schema map[string]any) *genai.Schema {
	s := &genai.Schema{}

	if t, ok := schema["type"].(string); ok {
		switch t {
		case "object":
			s.Type = genai.TypeObject
		case "string":
			s.Type = genai.TypeString
		case "number":
			s.Type = genai.TypeNumber
		case "integer":
			s.Type = genai.TypeInteger
		case "boolean":
			s.Type = genai.TypeBoolean
		case "array":
			s.Type = genai.TypeArray
		}
	}

	if desc, ok := schema["description"].(string); ok {
		s.Description = desc
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*genai.Schema)
		for name, prop := range props {
			if propMap, ok := prop.(map[string]any); ok {
				s.Properties[name] = jsonSchemaToGeminiSchema(propMap)
			}
		}
	}

	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				s = s // suppress unused warning
				// Gemini Schema doesn't have Required field directly,
				// but we can set it if available
			}
		}
		// Actually set Required
		for _, r := range req {
			if str, ok := r.(string); ok {
				s.Required = append(s.Required, str)
			}
		}
	}

	if items, ok := schema["items"].(map[string]any); ok {
		s.Items = jsonSchemaToGeminiSchema(items)
	}

	return s
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
		case ContentTypeToolUse:
			if part.ToolUse != nil {
				var args map[string]any
				if err := json.Unmarshal(part.ToolUse.Input, &args); err != nil {
					args = map[string]any{}
				}
				parts = append(parts, genai.FunctionCall{
					Name: part.ToolUse.Name,
					Args: args,
				})
			}
		case ContentTypeToolResult:
			if part.ToolResult != nil {
				response := map[string]any{"result": part.ToolResult.Content}
				if part.ToolResult.IsError {
					response["error"] = true
				}
				parts = append(parts, genai.FunctionResponse{
					Name:     part.ToolResult.ToolUseID, // Gemini uses the function name, not an ID
					Response: response,
				})
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

// extractContentAndBlocks extracts both text content and structured content blocks
func (p *GeminiProvider) extractContentAndBlocks(resp *genai.GenerateContentResponse) (string, []ContentBlock) {
	var content string
	var blocks []ContentBlock

	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			switch v := part.(type) {
			case genai.Text:
				text := string(v)
				content += text
				blocks = append(blocks, ContentBlock{
					Type: ContentTypeText,
					Text: text,
				})
			case genai.FunctionCall:
				callID := uuid.New().String()
				inputBytes, _ := json.Marshal(v.Args)
				blocks = append(blocks, ContentBlock{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:    callID,
						Name:  v.Name,
						Input: inputBytes,
					},
				})
			}
		}
	}

	return content, blocks
}
