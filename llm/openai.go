package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// openaiTraceEvents enables verbose logging of every Responses API stream
// event type. Useful for debugging reasoning-summary streaming on a new
// OpenAI-compatible server. Toggle by setting SQUADRON_OPENAI_TRACE=1.
var openaiTraceEvents = os.Getenv("SQUADRON_OPENAI_TRACE") != ""

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

// OpenAIProvider speaks to the OpenAI Responses API (`/v1/responses`) and
// any OpenAI-compatible server that implements it (Ollama 0.13.3+, recent
// vLLM, LiteLLM). Stateless mode only — full conversation history is sent
// every turn, no previous_response_id, so Ollama's non-stateful subset is
// sufficient.
type OpenAIProvider struct {
	client *openai.Client
}

func NewOpenAIProvider(apiKey, baseURL string) *OpenAIProvider {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := openai.NewClient(opts...)
	return &OpenAIProvider{client: &client}
}

// NewOpenAICompatibleProvider creates a provider that targets an OpenAI-compatible
// API at the given base URL (e.g. Ollama at http://localhost:11434/v1).
// Requires the server to implement /v1/responses (Ollama 0.13.3+, recent vLLM,
// LiteLLM with default routing).
func NewOpenAICompatibleProvider(baseURL string) *OpenAIProvider {
	client := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey("ollama"), // dummy key; local servers ignore it
	)
	return &OpenAIProvider{client: &client}
}

func (p *OpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	params, err := p.buildResponseParams(req)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Responses.New(ctx, params)
	if err != nil {
		return nil, err
	}

	content, contentBlocks := extractResponseOutput(resp)

	return &ChatResponse{
		ID:            resp.ID,
		Content:       content,
		ContentBlocks: contentBlocks,
		FinishReason:  string(resp.Status),
		Usage:         usageFromResponse(resp.Usage),
	}, nil
}

func (p *OpenAIProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	params, err := p.buildResponseParams(req)
	if err != nil {
		return nil, err
	}

	if openaiTraceEvents {
		// Log only the model + reasoning config so we can verify the request
		// shape without dumping prompts/tools to stdout.
		if reqJSON, err := json.Marshal(params); err == nil {
			var trimmed map[string]any
			_ = json.Unmarshal(reqJSON, &trimmed)
			delete(trimmed, "instructions")
			delete(trimmed, "input")
			delete(trimmed, "tools")
			if b, err := json.Marshal(trimmed); err == nil {
				log.Printf("[openai-trace] REQUEST_PARAMS %s", string(b))
			}
		}
	}

	stream := p.client.Responses.NewStreaming(ctx, params)

	// gpt-5 and o-series reason by default whether or not we asked for it.
	// When the agent didn't opt in via reasoning="...", suppress all
	// reasoning stream events so the UI/event log only shows reasoning
	// when the user explicitly enabled it.
	emitReasoning := req.Reasoning != ""

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)

		var contentBlocks []ContentBlock
		var stopReason string
		var finalUsage Usage

		// Track in-progress streamed items by output_index. The Responses
		// API sends arguments deltas keyed only by the item's output_index;
		// we cache the function call's id/name from output_item.added so we
		// can surface ToolCallStart/Done with full identity.
		type funcCallState struct {
			callID    string
			name      string
			arguments strings.Builder
		}
		funcCalls := make(map[int64]*funcCallState)

		// Reasoning text accumulator. The API emits reasoning in summary
		// "parts" — each part is independent text — so we coalesce parts
		// from the same item into a single ThinkingBlock and a single
		// ReasoningStarted/Completed event pair. We also capture the
		// item's ID and encrypted_content so the block can round-trip
		// back on subsequent stateless turns.
		type reasoningState struct {
			text             strings.Builder
			emitted          bool
			providerID       string
			encryptedContent string
		}
		reasoning := make(map[int64]*reasoningState)

		for stream.Next() {
			event := stream.Current()

			if openaiTraceEvents {
				log.Printf("[openai-trace] type=%s output_index=%d summary_index=%d item_type=%s delta_str_len=%d args_len=%d text_len=%d part_type=%s part_text_len=%d raw=%s",
					event.Type, event.OutputIndex, event.SummaryIndex, event.Item.Type,
					len(event.Delta.OfString), len(event.Arguments), len(event.Text),
					event.Part.Type, len(event.Part.Text),
					truncate(event.RawJSON(), 400))
			}

			switch event.Type {
			case "response.created":
				// nothing to surface

			case "response.output_item.added":
				// A new output item is starting. Could be a message,
				// function_call, or reasoning item.
				switch event.Item.Type {
				case "function_call":
					funcCalls[event.OutputIndex] = &funcCallState{
						callID: event.Item.CallID,
						name:   event.Item.Name,
					}
					chunks <- StreamChunk{
						ToolCallStart: &ToolCallStartChunk{
							ID:   event.Item.CallID,
							Name: event.Item.Name,
						},
					}
				case "reasoning":
					// Always allocate so we can capture provider_id /
					// encrypted_content even when reasoning is not surfaced
					// to the UI — the round-trip needs them regardless.
					rs := &reasoningState{providerID: event.Item.ID}
					reasoning[event.OutputIndex] = rs
					if emitReasoning {
						// Open the reasoning event window NOW — even if no
						// summary text follows. OpenAI o-series and gpt-5
						// models often reason without emitting summary
						// text; surfacing the start gives the UI a visible
						// signal that reasoning is in progress.
						chunks <- StreamChunk{ReasoningStart: true}
						rs.emitted = true
					}
				}

			case "response.output_text.delta":
				// Visible answer text streaming.
				if event.Delta.OfString != "" {
					chunks <- StreamChunk{Content: event.Delta.OfString}
				}

			case "response.reasoning_summary_text.delta",
				"response.reasoning_summary.delta":
				if !emitReasoning {
					break
				}
				// Reasoning summary is streamed incrementally. The first
				// non-empty delta opens the reasoning event window.
				rs, ok := reasoning[event.OutputIndex]
				if !ok {
					rs = &reasoningState{}
					reasoning[event.OutputIndex] = rs
				}
				if !rs.emitted {
					chunks <- StreamChunk{ReasoningStart: true}
					rs.emitted = true
				}
				if event.Delta.OfString != "" {
					text := event.Delta.OfString
					rs.text.WriteString(text)
					chunks <- StreamChunk{ReasoningDelta: text}
				}

			case "response.reasoning_summary_text.done",
				"response.reasoning_summary.done":
				// One summary part finished. There may be more parts in
				// the same reasoning item — close on output_item.done
				// instead so we capture them all into one ThinkingBlock.

			case "response.output_item.done":
				// Close any reasoning window when the reasoning output
				// item finishes — covers both the "summary text emitted"
				// and "internal reasoning, no summary" cases uniformly.
				if event.Item.Type == "reasoning" {
					rs, ok := reasoning[event.OutputIndex]
					if ok && rs.emitted {
						chunks <- StreamChunk{ReasoningDone: true}
					}
					if ok {
						// Pull the encrypted_content off the final item
						// (only present when include=reasoning.encrypted_content).
						if event.Item.EncryptedContent != "" {
							rs.encryptedContent = event.Item.EncryptedContent
						}
						if event.Item.ID != "" {
							rs.providerID = event.Item.ID
						}
						if rs.text.Len() > 0 || rs.providerID != "" || rs.encryptedContent != "" {
							contentBlocks = append(contentBlocks, ContentBlock{
								Type: ContentTypeThinking,
								Thinking: &ThinkingBlock{
									Text:             rs.text.String(),
									ProviderID:       rs.providerID,
									EncryptedContent: rs.encryptedContent,
								},
							})
						}
					}
					delete(reasoning, event.OutputIndex)
				}

			case "response.function_call_arguments.delta":
				fc, ok := funcCalls[event.OutputIndex]
				if !ok {
					break
				}
				if event.Delta.OfString != "" {
					fc.arguments.WriteString(event.Delta.OfString)
					chunks <- StreamChunk{ToolCallDelta: event.Delta.OfString}
				}

			case "response.function_call_arguments.done":
				fc, ok := funcCalls[event.OutputIndex]
				if !ok {
					break
				}
				args := fc.arguments.String()
				// The .done event sometimes carries the canonical full
				// arguments string; prefer it over our accumulated copy
				// to avoid drift if any deltas were dropped.
				if event.Arguments != "" {
					args = event.Arguments
				}
				contentBlocks = append(contentBlocks, ContentBlock{
					Type: ContentTypeToolUse,
					ToolUse: &ToolUseBlock{
						ID:    fc.callID,
						Name:  fc.name,
						Input: json.RawMessage(args),
					},
				})
				id := fc.callID
				chunks <- StreamChunk{ToolCallDone: &id}
				delete(funcCalls, event.OutputIndex)

			case "response.completed":
				// Final event with full Response object — captures usage
				// and the canonical stop reason.
				stopReason = string(event.Response.Status)
				finalUsage = usageFromResponse(event.Response.Usage)

			case "response.failed", "response.incomplete":
				stopReason = string(event.Response.Status)
				if event.Response.Error.Message != "" {
					chunks <- StreamChunk{
						Error: fmt.Errorf("openai response %s: %s", event.Type, event.Response.Error.Message),
						Done:  true,
					}
					return
				}

			case "error":
				chunks <- StreamChunk{
					Error: fmt.Errorf("openai stream error: %s", event.Message),
					Done:  true,
				}
				return
			}
		}

		if err := stream.Err(); err != nil {
			chunks <- StreamChunk{Error: err, Done: true}
			return
		}

		// Final aggregated done chunk.
		chunks <- StreamChunk{
			Done:          true,
			Usage:         &finalUsage,
			StopReason:    stopReason,
			ContentBlocks: contentBlocks,
		}
	}()

	return chunks, nil
}

// buildResponseParams converts our provider-agnostic ChatRequest into a
// ResponseNewParams ready for the SDK. System messages collapse into
// Instructions; everything else maps to ResponseInputItemUnionParam.
func (p *OpenAIProvider) buildResponseParams(req *ChatRequest) (responses.ResponseNewParams, error) {
	instructions, items := p.convertMessages(req.Messages)

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(req.Model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: items},
	}

	if instructions != "" {
		params.Instructions = param.NewOpt(instructions)
	}

	if req.MaxTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(int64(req.MaxTokens))
	}

	if req.Temperature > 0 {
		params.Temperature = param.NewOpt(req.Temperature)
	}

	if effort := openAIReasoningEffort(req.Reasoning); effort != "" {
		// Use "detailed" so reasoning summaries are emitted on every turn
		// where reasoning actually happened. "auto" lets the model skip
		// the summary on simple turns, which means UI consumers see no
		// reasoning text even though tokens were spent on reasoning.
		params.Reasoning = shared.ReasoningParam{
			Effort:  effort,
			Summary: shared.ReasoningSummaryDetailed,
		}
		// Ask the API to return encrypted_content on every reasoning item so
		// we can echo prior reasoning back on subsequent stateless turns —
		// without it, multi-turn reasoning loses context.
		params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
	}

	if len(req.Tools) > 0 {
		params.Tools = p.convertTools(req.Tools)
	}

	// Stateless mode — we send full conversation history every turn. Avoids
	// any dependence on previous_response_id (which Ollama doesn't support).
	params.Store = param.NewOpt(false)

	return params, nil
}

// convertMessages walks the conversation history and produces (a) a single
// instructions string from system messages and (b) a list of input items
// for the Responses API.
func (p *OpenAIProvider) convertMessages(messages []Message) (string, responses.ResponseInputParam) {
	var sysBuf strings.Builder
	var items responses.ResponseInputParam

	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			if sysBuf.Len() > 0 {
				sysBuf.WriteString("\n\n")
			}
			sysBuf.WriteString(m.GetTextContent())
		case RoleUser:
			items = append(items, p.convertUserMessage(m)...)
		case RoleAssistant:
			items = append(items, p.convertAssistantMessage(m)...)
		}
	}

	return sysBuf.String(), items
}

// convertUserMessage emits zero or more input items for a single user-role
// message. A bundle of tool results expands into N function_call_output
// items (one per tool call); plain text/image content collapses to a single
// input message.
func (p *OpenAIProvider) convertUserMessage(m Message) []responses.ResponseInputItemUnionParam {
	if m.HasParts() {
		// All-tool-results bundle → one output item per result.
		allToolResults := true
		for _, part := range m.Parts {
			if part.Type != ContentTypeToolResult {
				allToolResults = false
				break
			}
		}
		if allToolResults && len(m.Parts) > 0 {
			out := make([]responses.ResponseInputItemUnionParam, 0, len(m.Parts))
			for _, part := range m.Parts {
				tr := part.ToolResult
				if tr == nil {
					continue
				}
				out = append(out, responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: tr.ToolUseID,
						Output: tr.Content,
					},
				})
			}
			return out
		}
	}

	// Plain text content
	if !m.HasParts() {
		if m.Content == "" {
			return nil
		}
		return []responses.ResponseInputItemUnionParam{
			responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleUser),
		}
	}

	// Multimodal content — collapse to a single input message with a
	// content list (text + image parts).
	var content responses.ResponseInputMessageContentListParam
	for _, part := range m.Parts {
		switch part.Type {
		case ContentTypeText:
			content = append(content, responses.ResponseInputContentUnionParam{
				OfInputText: &responses.ResponseInputTextParam{Text: part.Text},
			})
		case ContentTypeImage:
			if part.ImageData != nil {
				dataURL := fmt.Sprintf("data:%s;base64,%s", part.ImageData.MediaType, part.ImageData.Data)
				content = append(content, responses.ResponseInputContentUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						ImageURL: param.NewOpt(dataURL),
						Detail:   responses.ResponseInputImageDetailAuto,
					},
				})
			}
		}
	}
	if len(content) == 0 {
		return nil
	}
	return []responses.ResponseInputItemUnionParam{
		responses.ResponseInputItemParamOfMessage(content, responses.EasyInputMessageRoleUser),
	}
}

// convertAssistantMessage emits zero or more input items for a single
// assistant-role message. Text becomes an assistant message item; each
// tool_use block becomes a function_call item; thinking blocks with a
// provider_id (i.e. captured from this same provider) are echoed back as
// reasoning items so stateless multi-turn reasoning round-trips correctly.
// Thinking blocks without a provider_id were emitted by a different
// provider and are dropped silently.
func (p *OpenAIProvider) convertAssistantMessage(m Message) []responses.ResponseInputItemUnionParam {
	if !m.HasParts() {
		if text := m.GetTextContent(); text != "" {
			return []responses.ResponseInputItemUnionParam{
				responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleAssistant),
			}
		}
		return nil
	}

	var out []responses.ResponseInputItemUnionParam
	var textBuf strings.Builder
	for _, part := range m.Parts {
		switch part.Type {
		case ContentTypeText:
			textBuf.WriteString(part.Text)
		case ContentTypeToolUse:
			if part.ToolUse != nil {
				out = append(out, responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						CallID:    part.ToolUse.ID,
						Name:      part.ToolUse.Name,
						Arguments: string(part.ToolUse.Input),
					},
				})
			}
		case ContentTypeThinking:
			if part.Thinking != nil && part.Thinking.ProviderID != "" {
				rp := &responses.ResponseReasoningItemParam{ID: part.Thinking.ProviderID}
				if part.Thinking.Text != "" {
					rp.Summary = []responses.ResponseReasoningItemSummaryParam{{Text: part.Thinking.Text}}
				}
				if part.Thinking.EncryptedContent != "" {
					rp.EncryptedContent = param.NewOpt(part.Thinking.EncryptedContent)
				}
				out = append(out, responses.ResponseInputItemUnionParam{OfReasoning: rp})
			}
		case ContentTypeProviderRaw:
			// Reserved for future provider-managed tool blocks. Only echo
			// back when this raw block originated from OpenAI.
			_ = part
		}
	}
	if textBuf.Len() > 0 {
		out = append(out, responses.ResponseInputItemParamOfMessage(textBuf.String(), responses.EasyInputMessageRoleAssistant))
	}
	return out
}

// convertTools maps provider-agnostic tool definitions into the function
// tool params that Responses API expects (note: flat shape, not nested
// under a {function: {...}} envelope like Chat Completions).
func (p *OpenAIProvider) convertTools(tools []ToolDefinition) []responses.ToolUnionParam {
	result := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var params map[string]any
		if err := json.Unmarshal(t.InputSchema, &params); err != nil {
			continue
		}
		// strict=false — Squadron-defined schemas are not always closed-world
		// compatible with OpenAI's strict mode (e.g., free-form objects).
		result = append(result, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        t.Name,
				Description: param.NewOpt(t.Description),
				Parameters:  params,
				Strict:      param.NewOpt(false),
			},
		})
	}
	return result
}

// extractResponseOutput walks a non-streaming Response and produces the
// canonical text content + structured ContentBlocks (text, thinking, tool_use).
func extractResponseOutput(resp *responses.Response) (string, []ContentBlock) {
	var textBuf strings.Builder
	var blocks []ContentBlock

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			// Output message — collect text content from all content parts.
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					textBuf.WriteString(c.Text)
				}
			}
		case "function_call":
			blocks = append(blocks, ContentBlock{
				Type: ContentTypeToolUse,
				ToolUse: &ToolUseBlock{
					ID:    item.CallID,
					Name:  item.Name,
					Input: json.RawMessage(item.Arguments),
				},
			})
		case "reasoning":
			// Concatenate all summary parts into one ThinkingBlock. We
			// capture the provider's reasoning ID and encrypted_content
			// even when the summary is empty — both are required to
			// round-trip stateless multi-turn reasoning back to the API.
			var rb strings.Builder
			for _, s := range item.Summary {
				if rb.Len() > 0 {
					rb.WriteString("\n")
				}
				rb.WriteString(s.Text)
			}
			if rb.Len() > 0 || item.ID != "" || item.EncryptedContent != "" {
				blocks = append(blocks, ContentBlock{
					Type: ContentTypeThinking,
					Thinking: &ThinkingBlock{
						Text:             rb.String(),
						ProviderID:       item.ID,
						EncryptedContent: item.EncryptedContent,
					},
				})
			}
		}
	}

	text := textBuf.String()
	if text != "" {
		// Prepend a text content block so callers that read ContentBlocks
		// (without falling back to Content) see the visible answer.
		blocks = append([]ContentBlock{{Type: ContentTypeText, Text: text}}, blocks...)
	}

	return text, blocks
}

func usageFromResponse(u responses.ResponseUsage) Usage {
	usage := Usage{
		InputTokens:  int(u.InputTokens),
		OutputTokens: int(u.OutputTokens),
	}
	if u.InputTokensDetails.CachedTokens > 0 {
		usage.CacheReadTokens = int(u.InputTokensDetails.CachedTokens)
		usage.InputTokens -= usage.CacheReadTokens
		if usage.InputTokens < 0 {
			usage.InputTokens = 0
		}
	}
	return usage
}

