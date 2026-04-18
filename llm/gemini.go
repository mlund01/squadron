package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"
	"google.golang.org/genai"
)

type GeminiProvider struct {
	client *genai.Client
}

func NewGeminiProvider(ctx context.Context, apiKey, baseURL string) (*GeminiProvider, error) {
	cfg := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if baseURL != "" {
		cfg.HTTPOptions = genai.HTTPOptions{BaseURL: baseURL}
	}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &GeminiProvider{client: client}, nil
}

// Close is a no-op for the new genai SDK (no persistent client state to release).
func (p *GeminiProvider) Close() error { return nil }

func (p *GeminiProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	contents, sysInstr := p.buildContents(req.Messages)
	cfg := p.buildConfig(req, sysInstr)

	resp, err := p.client.Models.GenerateContent(ctx, req.Model, contents, cfg)
	if err != nil {
		return nil, err
	}

	content, blocks := p.extractResponse(resp)
	finish := ""
	if len(resp.Candidates) > 0 {
		finish = string(resp.Candidates[0].FinishReason)
	}

	return &ChatResponse{
		ID:            uuid.New().String(),
		Content:       content,
		ContentBlocks: blocks,
		FinishReason:  finish,
		Usage:         usageFromGemini(resp.UsageMetadata),
	}, nil
}

func (p *GeminiProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	contents, sysInstr := p.buildContents(req.Messages)
	cfg := p.buildConfig(req, sysInstr)

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		var allBlocks []ContentBlock
		var usage Usage
		var stopReason string

		for resp, err := range p.client.Models.GenerateContentStream(ctx, req.Model, contents, cfg) {
			if err != nil {
				chunks <- StreamChunk{Error: err, Done: true}
				return
			}
			if resp.UsageMetadata != nil {
				usage = usageFromGemini(resp.UsageMetadata)
			}
			for _, cand := range resp.Candidates {
				if cand.FinishReason != "" {
					stopReason = string(cand.FinishReason)
				}
				if cand.Content == nil {
					continue
				}
				for _, part := range cand.Content.Parts {
					if part == nil {
						continue
					}
					// Skip thought parts — we don't surface model reasoning.
					// Their signatures travel with the function call parts that follow.
					if part.Thought {
						continue
					}
					if part.FunctionCall != nil {
						callID := part.FunctionCall.ID
						if callID == "" {
							callID = uuid.New().String()
						}
						inputBytes, _ := json.Marshal(part.FunctionCall.Args)

						chunks <- StreamChunk{
							ToolCallStart: &ToolCallStartChunk{ID: callID, Name: part.FunctionCall.Name},
						}
						chunks <- StreamChunk{ToolCallDelta: string(inputBytes)}

						allBlocks = append(allBlocks, ContentBlock{
							Type: ContentTypeToolUse,
							ToolUse: &ToolUseBlock{
								ID:               callID,
								Name:             part.FunctionCall.Name,
								Input:            inputBytes,
								ThoughtSignature: part.ThoughtSignature,
							},
						})

						chunks <- StreamChunk{ToolCallDone: &callID}
						continue
					}
					if part.Text != "" {
						chunks <- StreamChunk{Content: part.Text}
						if n := len(allBlocks); n > 0 && allBlocks[n-1].Type == ContentTypeText {
							allBlocks[n-1].Text += part.Text
						} else {
							allBlocks = append(allBlocks, ContentBlock{Type: ContentTypeText, Text: part.Text})
						}
					}
				}
			}
		}

		chunks <- StreamChunk{
			Done:          true,
			Usage:         &usage,
			StopReason:    stopReason,
			ContentBlocks: allBlocks,
		}
	}()

	return chunks, nil
}

func usageFromGemini(u *genai.GenerateContentResponseUsageMetadata) Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:     int(u.PromptTokenCount),
		OutputTokens:    int(u.CandidatesTokenCount),
		CacheReadTokens: int(u.CachedContentTokenCount),
	}
}

func (p *GeminiProvider) buildConfig(req *ChatRequest, sysInstr *genai.Content) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{}
	if sysInstr != nil {
		cfg.SystemInstruction = sysInstr
	}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = int32(req.MaxTokens)
	}
	if req.Temperature > 0 {
		t := float32(req.Temperature)
		cfg.Temperature = &t
	}
	if len(req.StopSequences) > 0 {
		cfg.StopSequences = req.StopSequences
	}
	if len(req.Tools) > 0 {
		cfg.Tools = convertGeminiTools(req.Tools)
	}
	return cfg
}

func (p *GeminiProvider) buildContents(messages []Message) ([]*genai.Content, *genai.Content) {
	var sysInstr *genai.Content
	var contents []*genai.Content

	// Map tool_use ID → function name so we can populate FunctionResponse.Name
	// correctly when we encounter the matching tool result later in history.
	toolNames := map[string]string{}
	for _, m := range messages {
		for _, block := range m.Parts {
			if block.Type == ContentTypeToolUse && block.ToolUse != nil {
				toolNames[block.ToolUse.ID] = block.ToolUse.Name
			}
		}
	}

	for _, m := range messages {
		if m.Role == RoleSystem {
			text := m.GetTextContent()
			if text == "" {
				continue
			}
			if sysInstr == nil {
				sysInstr = &genai.Content{Parts: []*genai.Part{{Text: text}}}
			} else {
				sysInstr.Parts = append(sysInstr.Parts, &genai.Part{Text: text})
			}
			continue
		}

		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}

		parts := messageToParts(m, toolNames)
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, &genai.Content{Role: role, Parts: parts})
	}

	return contents, sysInstr
}

func messageToParts(m Message, toolNames map[string]string) []*genai.Part {
	if !m.HasParts() {
		if m.Content == "" {
			return nil
		}
		return []*genai.Part{{Text: m.Content}}
	}

	var parts []*genai.Part
	for _, block := range m.Parts {
		switch block.Type {
		case ContentTypeText:
			if block.Text != "" {
				parts = append(parts, &genai.Part{Text: block.Text})
			}
		case ContentTypeImage:
			if block.ImageData != nil {
				data, err := base64.StdEncoding.DecodeString(block.ImageData.Data)
				if err == nil {
					parts = append(parts, &genai.Part{
						InlineData: &genai.Blob{MIMEType: block.ImageData.MediaType, Data: data},
					})
				}
			}
		case ContentTypeToolUse:
			if block.ToolUse != nil {
				var args map[string]any
				if err := json.Unmarshal(block.ToolUse.Input, &args); err != nil {
					args = map[string]any{}
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						ID:   block.ToolUse.ID,
						Name: block.ToolUse.Name,
						Args: args,
					},
					ThoughtSignature: block.ToolUse.ThoughtSignature,
				})
			}
		case ContentTypeToolResult:
			if block.ToolResult != nil {
				response := map[string]any{"result": block.ToolResult.Content}
				if block.ToolResult.IsError {
					response["error"] = true
				}
				name := toolNames[block.ToolResult.ToolUseID]
				parts = append(parts, &genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:       block.ToolResult.ToolUseID,
						Name:     name,
						Response: response,
					},
				})
			}
		}
	}
	return parts
}

func (p *GeminiProvider) extractResponse(resp *genai.GenerateContentResponse) (string, []ContentBlock) {
	var content string
	var blocks []ContentBlock

	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part == nil || part.Thought {
				continue
			}
			if part.FunctionCall != nil {
				callID := part.FunctionCall.ID
				if callID == "" {
					callID = uuid.New().String()
				}
				inputBytes, _ := json.Marshal(part.FunctionCall.Args)
				blocks = append(blocks, ContentBlock{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:               callID,
						Name:             part.FunctionCall.Name,
						Input:            inputBytes,
						ThoughtSignature: part.ThoughtSignature,
					},
				})
				continue
			}
			if part.Text != "" {
				content += part.Text
				if n := len(blocks); n > 0 && blocks[n-1].Type == ContentTypeText {
					blocks[n-1].Text += part.Text
				} else {
					blocks = append(blocks, ContentBlock{Type: ContentTypeText, Text: part.Text})
				}
			}
		}
	}
	return content, blocks
}

func convertGeminiTools(tools []ToolDefinition) []*genai.Tool {
	var decls []*genai.FunctionDeclaration
	for _, t := range tools {
		fd := &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
		}
		var schemaMap map[string]any
		if err := json.Unmarshal(t.InputSchema, &schemaMap); err == nil {
			fd.Parameters = jsonSchemaToGeminiSchema(schemaMap)
		}
		decls = append(decls, fd)
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// jsonSchemaToGeminiSchema converts a generic JSON Schema map into the
// genai.Schema structure. Gemini accepts a well-defined subset of OpenAPI
// schema; unsupported keywords (oneOf, allOf, $ref, additionalProperties,
// etc.) are dropped silently.
func jsonSchemaToGeminiSchema(schema map[string]any) *genai.Schema {
	s := &genai.Schema{}

	// type — JSON Schema allows an array of types (e.g. ["string", "null"]).
	// Gemini has no union type; map ["X", "null"] to X + nullable=true, and
	// fall back to anyOf for multi-type unions.
	if t, ok := schema["type"]; ok {
		applyJSONSchemaType(s, t)
	}

	if title, ok := schema["title"].(string); ok {
		s.Title = title
	}
	if desc, ok := schema["description"].(string); ok {
		s.Description = desc
	}
	if format, ok := schema["format"].(string); ok {
		s.Format = format
	}
	if pattern, ok := schema["pattern"].(string); ok {
		s.Pattern = pattern
	}
	if def, ok := schema["default"]; ok {
		s.Default = def
	}
	if ex, ok := schema["example"]; ok {
		s.Example = ex
	}
	if n, ok := schema["nullable"].(bool); ok {
		s.Nullable = &n
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*genai.Schema, len(props))
		for name, prop := range props {
			if propMap, ok := prop.(map[string]any); ok {
				s.Properties[name] = jsonSchemaToGeminiSchema(propMap)
			}
		}
	}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if str, ok := r.(string); ok {
				s.Required = append(s.Required, str)
			}
		}
	}
	if order, ok := schema["propertyOrdering"].([]any); ok {
		for _, p := range order {
			if str, ok := p.(string); ok {
				s.PropertyOrdering = append(s.PropertyOrdering, str)
			}
		}
	}

	if items, ok := schema["items"].(map[string]any); ok {
		s.Items = jsonSchemaToGeminiSchema(items)
	}

	// enum — JSON Schema allows any value type; Gemini expects strings.
	// Stringify non-string values so int/bool enums survive the round trip.
	if enum, ok := schema["enum"].([]any); ok {
		s.Enum = make([]string, 0, len(enum))
		for _, v := range enum {
			switch tv := v.(type) {
			case string:
				s.Enum = append(s.Enum, tv)
			case nil:
				s.Enum = append(s.Enum, "")
			default:
				b, err := json.Marshal(tv)
				if err == nil {
					s.Enum = append(s.Enum, string(b))
				}
			}
		}
		// Gemini docs recommend format="enum" when enum is set on non-string types.
		if s.Format == "" {
			s.Format = "enum"
		}
	}

	if anyOf, ok := schema["anyOf"].([]any); ok {
		for _, sub := range anyOf {
			if subMap, ok := sub.(map[string]any); ok {
				s.AnyOf = append(s.AnyOf, jsonSchemaToGeminiSchema(subMap))
			}
		}
	}
	// oneOf is treated as anyOf (Gemini has no oneOf equivalent).
	if oneOf, ok := schema["oneOf"].([]any); ok {
		for _, sub := range oneOf {
			if subMap, ok := sub.(map[string]any); ok {
				s.AnyOf = append(s.AnyOf, jsonSchemaToGeminiSchema(subMap))
			}
		}
	}

	// Numeric bounds.
	if v, ok := toFloat64(schema["minimum"]); ok {
		s.Minimum = &v
	}
	if v, ok := toFloat64(schema["maximum"]); ok {
		s.Maximum = &v
	}
	// Length/size bounds.
	if v, ok := toInt64(schema["minLength"]); ok {
		s.MinLength = &v
	}
	if v, ok := toInt64(schema["maxLength"]); ok {
		s.MaxLength = &v
	}
	if v, ok := toInt64(schema["minItems"]); ok {
		s.MinItems = &v
	}
	if v, ok := toInt64(schema["maxItems"]); ok {
		s.MaxItems = &v
	}
	if v, ok := toInt64(schema["minProperties"]); ok {
		s.MinProperties = &v
	}
	if v, ok := toInt64(schema["maxProperties"]); ok {
		s.MaxProperties = &v
	}

	return s
}

// applyJSONSchemaType handles the `type` keyword, which JSON Schema allows to
// be either a single string or an array. Gemini's Type is a single value;
// ["X", "null"] collapses to X + nullable, and broader unions fall through to
// anyOf (populated elsewhere).
func applyJSONSchemaType(s *genai.Schema, t any) {
	switch tv := t.(type) {
	case string:
		s.Type = jsonTypeToGemini(tv)
	case []any:
		var nonNull []string
		hasNull := false
		for _, entry := range tv {
			if str, ok := entry.(string); ok {
				if str == "null" {
					hasNull = true
				} else {
					nonNull = append(nonNull, str)
				}
			}
		}
		if hasNull {
			n := true
			s.Nullable = &n
		}
		if len(nonNull) == 1 {
			s.Type = jsonTypeToGemini(nonNull[0])
		} else if len(nonNull) > 1 {
			// Union type: expand into AnyOf branches, one per type.
			for _, name := range nonNull {
				s.AnyOf = append(s.AnyOf, &genai.Schema{Type: jsonTypeToGemini(name)})
			}
		}
	}
}

func jsonTypeToGemini(t string) genai.Type {
	switch t {
	case "object":
		return genai.TypeObject
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	}
	return ""
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
