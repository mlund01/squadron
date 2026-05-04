package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/aitools"
	"squadron/llm"
	"squadron/streamers"
)

// secretPattern matches ${secrets.name} placeholders in tool inputs
var secretPattern = regexp.MustCompile(`\$\{secrets\.([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// secretInjector replaces ${secrets.name} placeholders with actual values
type secretInjector struct {
	secrets map[string]string
}

// newSecretInjector creates a new secret injector
func newSecretInjector(secrets map[string]string) *secretInjector {
	if secrets == nil {
		secrets = make(map[string]string)
	}
	return &secretInjector{secrets: secrets}
}

// Inject replaces all ${secrets.name} placeholders with actual values
// Returns error if an unknown secret is referenced
func (si *secretInjector) Inject(input string) (string, error) {
	var lastErr error
	result := secretPattern.ReplaceAllStringFunc(input, func(match string) string {
		// Extract secret name from ${secrets.name}
		submatches := secretPattern.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		secretName := submatches[1]

		value, ok := si.secrets[secretName]
		if !ok {
			lastErr = fmt.Errorf("unknown secret: %s", secretName)
			return match // Keep placeholder if not found
		}
		return value
	})

	return result, lastErr
}

// llmSession defines the interface for LLM session operations needed by the orchestrator
type llmSession interface {
	// SendStream sends a message and streams the response, calling onChunk for each chunk
	SendStream(ctx context.Context, userMessage string, onChunk func(chunk llm.StreamChunk)) (*llm.ChatResponse, error)
	// SendMessageStream sends a multimodal message and streams the response
	SendMessageStream(ctx context.Context, msg llm.Message, onChunk func(chunk llm.StreamChunk)) (*llm.ChatResponse, error)
	// ContinueStream resumes from existing session state without adding a new user message
	ContinueStream(ctx context.Context, onChunk func(chunk llm.StreamChunk)) (*llm.ChatResponse, error)
	// AddToolResults appends tool result messages to the session history
	AddToolResults(results []llm.ToolResultBlock)
}

// orchestrator handles the agent conversation loop
type orchestrator struct {
	session        llmSession
	streamer       streamers.ChatHandler
	tools          map[string]aitools.Tool
	interceptor    *aitools.ResultInterceptor
	pruningManager *llm.PruningManager
	eventLogger    EventLogger
	turnLogger     *llm.TurnLogger
	secretInjector *secretInjector
	compaction     *CompactionConfig
	onCompaction   func(inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int)
	onSessionTurn  func(data protocol.SessionTurnData)
	modelName      string
	sessionLogger    SessionLogger
	sessionID        string
	taskID           string
	pricingOverrides map[string]*llm.ModelPricing
	budget           BudgetChecker
	maxTokensRetries int // Count of consecutive max_tokens truncation retries
}

// newOrchestrator creates a new chat orchestrator
func newOrchestrator(session llmSession, streamer streamers.ChatHandler, tools map[string]aitools.Tool, interceptor *aitools.ResultInterceptor, pruningManager *llm.PruningManager, eventLogger EventLogger, turnLogger *llm.TurnLogger, secretValues map[string]string, compaction *CompactionConfig) *orchestrator {
	return &orchestrator{
		session:        session,
		streamer:       streamer,
		tools:          tools,
		interceptor:    interceptor,
		pruningManager: pruningManager,
		eventLogger:    eventLogger,
		turnLogger:     turnLogger,
		secretInjector: newSecretInjector(secretValues),
		compaction:     compaction,
	}
}

// processTurn handles a single conversation turn, including any tool calls.
// When resume=true, the first LLM call uses ContinueStream (no new user message)
// because the session already has a pending user message from healing or interruption.
// Returns a ChatResult with either an answer (complete) or ASK_COMMANDER question (needs input)
func (o *orchestrator) processTurn(ctx context.Context, input string, resume bool) (ChatResult, error) {
	var currentParts []llm.ContentBlock
	currentParts = append(currentParts, llm.ContentBlock{Type: llm.ContentTypeText, Text: input})
	var finalAnswer string
	firstTurn := true

	for {
		if o.budget != nil {
			if err := o.budget.CheckBudget(); err != nil {
				o.streamer.Error(err)
				return ChatResult{}, err
			}
		}

		// Create parser for streaming text content (ANSWER tags). Reasoning
		// is emitted via dedicated StreamChunk fields (see onChunk below).
		parser := NewMessageParser(o.streamer)

		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_llm_start", nil)
		}
		llmStart := time.Now()

		var resp *llm.ChatResponse
		var err error

		relay := newReasoningRelay(o.streamer.ReasoningStarted, o.streamer.PublishReasoningChunk, o.streamer.ReasoningCompleted)

		onChunk := func(chunk llm.StreamChunk) {
			relay.Handle(chunk)
			if chunk.Content != "" {
				// Visible answer text closes the reasoning window before
				// the parser sees it — the agent ANSWER protocol shouldn't
				// be tangled up in a reasoning span.
				relay.Close()
				parser.ProcessChunk(chunk.Content)
			}
		}

		if resume {
			// First turn of resume — LLM responds to existing state.
			resp, err = o.session.ContinueStream(ctx, onChunk)
			resume = false
		} else if firstTurn {
			// First turn — send the user input
			// Check if we have images in the content parts
			hasImages := false
			for _, p := range currentParts {
				if p.Type == llm.ContentTypeImage {
					hasImages = true
					break
				}
			}

			// Extract text for logging
			var textContent string
			for _, p := range currentParts {
				if p.Type == llm.ContentTypeText {
					textContent += p.Text
				}
			}

			// Log user message to session store with structured parts so resume
			// can rebuild the message faithfully (images, tool results, etc.).
			if o.sessionLogger != nil && o.sessionID != "" {
				now := time.Now()
				userMsg := llm.Message{Role: llm.RoleUser}
				if hasImages {
					userMsg.Parts = currentParts
				} else {
					userMsg.Content = textContent
				}
				parts := PartsFromMessage(userMsg)
				audit := AuditContentForMessage(userMsg)
				if audit == "" {
					audit = input
				}
				o.sessionLogger.AppendStructuredMessage(o.sessionID, "user", audit, parts, now, now)
			}

			if hasImages {
				msg := llm.NewMultimodalMessage(llm.RoleUser, currentParts...)
				resp, err = o.session.SendMessageStream(ctx, msg, onChunk)
			} else {
				resp, err = o.session.SendStream(ctx, textContent, onChunk)
			}
			firstTurn = false
		} else {
			// Subsequent turns — tool results are already in the session via AddToolResults.
			// Use ContinueStream since the tool results serve as implicit continuation.
			resp, err = o.session.ContinueStream(ctx, onChunk)
		}

		// Check if compaction is needed after response
		if resp != nil {
			o.checkAndCompact(resp.Usage.InputTokens)
		}

		// Apply threshold-based pruning after each response
		o.applyTurnPruning()

		// Emit session turn telemetry
		if resp != nil && o.onSessionTurn != nil {
			if adapter, ok := o.session.(*llm.SessionAdapter); ok {
				stats := adapter.GetSession().MessageStats()
				turnData := protocol.SessionTurnData{
					Model:             o.modelName,
					InputTokens:      resp.Usage.InputTokens,
					OutputTokens:     resp.Usage.OutputTokens,
					CacheWriteTokens: resp.Usage.CacheWriteTokens,
					CacheReadTokens:  resp.Usage.CacheReadTokens,
					UserMessages:     stats.UserCount,
					AssistantMessages: stats.AssistantCount,
					SystemMessages:   stats.SystemCount,
					PayloadBytes:     stats.PayloadBytes,
					TurnDurationMs:   time.Since(llmStart).Milliseconds(),
				}
				if pricing := llm.GetPricing(o.modelName, o.pricingOverrides); pricing != nil {
					cost := llm.ComputeTurnCost(pricing, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens)
					turnData.Cost = cost.TotalCost
					turnData.InputCost = cost.InputCost
					turnData.OutputCost = cost.OutputCost
					turnData.CacheReadCost = cost.CacheReadCost
					turnData.CacheWriteCost = cost.CacheWriteCost
				}
				o.onSessionTurn(turnData)
			}
		}

		if o.budget != nil && resp != nil {
			var turnCost float64
			if pricing := llm.GetPricing(o.modelName, o.pricingOverrides); pricing != nil {
				c := llm.ComputeTurnCost(pricing, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens)
				turnCost = c.TotalCost
			}
			if err := o.budget.RecordUsage(resp.Usage.Total(), turnCost); err != nil {
				relay.Close()
				parser.Finish()
				o.streamer.Error(err)
				return ChatResult{}, err
			}
		}

		if o.eventLogger != nil {
			eventData := map[string]any{
				"duration_ms": time.Since(llmStart).Milliseconds(),
			}
			if resp != nil {
				eventData["input_tokens"] = resp.Usage.InputTokens
				eventData["output_tokens"] = resp.Usage.OutputTokens
				if resp.Usage.CacheWriteTokens > 0 {
					eventData["cache_write_tokens"] = resp.Usage.CacheWriteTokens
				}
				if resp.Usage.CacheReadTokens > 0 {
					eventData["cache_read_tokens"] = resp.Usage.CacheReadTokens
				}
			}
			o.eventLogger.LogEvent("agent_llm_end", eventData)
		}

		relay.Close()
		parser.Finish()

		if err != nil {
			o.streamer.Error(err)
			return ChatResult{}, err
		}

		// Log assistant response to session store with structured parts so
		// thinking/tool_use blocks round-trip faithfully on resume.
		if o.sessionLogger != nil && o.sessionID != "" && resp != nil {
			asstMsg := llm.Message{Role: llm.RoleAssistant, Parts: resp.ContentBlocks, Content: resp.Content}
			parts := PartsFromMessage(asstMsg)
			audit := AuditContentForMessage(asstMsg)
			o.sessionLogger.AppendStructuredMessage(o.sessionID, "assistant", audit, parts, llmStart, time.Now())
		}

		// Extract tool calls from the response ContentBlocks
		var toolUses []llm.ToolUseBlock
		if resp != nil {
			for _, block := range resp.ContentBlocks {
				if block.Type == llm.ContentTypeToolUse && block.ToolUse != nil {
					toolUses = append(toolUses, *block.ToolUse)
				}
			}
		}

		// Determine first tool name for turn logging
		var firstToolName string
		if len(toolUses) > 0 {
			firstToolName = toolUses[0].Name
		}

		// Log turn snapshot after LLM response is in the session
		if o.turnLogger != nil {
			o.turnLogger.LogTurn(firstToolName, o.getSessionMessages())
		}

		// Capture the answer if one was provided via <ANSWER> tag
		if answer := parser.GetAnswer(); answer != "" {
			finalAnswer = answer
		}

		// Handle max_tokens truncation before processing tool uses. Any partial
		// tool_use can't be trusted (JSON input may be cut off), so we emit
		// error tool_results to keep the session protocol-valid and ask the
		// LLM to be more concise. Allow up to 3 corrective attempts.
		for resp != nil && resp.FinishReason == "max_tokens" {
			o.maxTokensRetries++
			if len(toolUses) > 0 {
				var errResults []llm.ToolResultBlock
				for _, tc := range toolUses {
					errResults = append(errResults, llm.ToolResultBlock{
						ToolUseID: tc.ID,
						Content:   "Error: tool call was truncated because the response hit the max output token limit; not executed.",
						IsError:   true,
					})
				}
				o.session.AddToolResults(errResults)
				toolUses = nil
			}
			if o.maxTokensRetries > 3 {
				return ChatResult{}, fmt.Errorf("agent response hit max output tokens after 3 correction attempts")
			}
			log.Printf("[Agent] Response hit max_tokens (attempt %d/3), sending correction...", o.maxTokensRetries)
			correction := "Your previous response hit the maximum output token limit and was truncated. Be more concise: shorten your reasoning, split the work into smaller tool calls, or finish with <ANSWER>...</ANSWER> if you have enough context."
			resp, err = o.session.SendStream(ctx, correction, onChunk)
			if err != nil {
				o.streamer.Error(err)
				return ChatResult{}, err
			}
			if resp != nil {
				for _, block := range resp.ContentBlocks {
					if block.Type == llm.ContentTypeToolUse && block.ToolUse != nil {
						toolUses = append(toolUses, *block.ToolUse)
					}
				}
				if answer := parser.GetAnswer(); answer != "" {
					finalAnswer = answer
				}
			}
		}
		o.maxTokensRetries = 0

		// If no tool calls, we're done with this turn
		if len(toolUses) == 0 {
			break
		}

		// Check for cancellation before executing tools
		if ctx.Err() != nil {
			return ChatResult{}, ctx.Err()
		}

		// Execute all tool calls and collect results
		var toolResults []llm.ToolResultBlock
		for _, tc := range toolUses {
			actionInput := string(tc.Input)

			// Check for ask_commander tool (intercepted, not actually executed)
			if tc.Name == "ask_commander" {
				// Parse the question from the input
				var askInput struct {
					Question string `json:"question"`
				}
				if jsonErr := json.Unmarshal(tc.Input, &askInput); jsonErr == nil && askInput.Question != "" {
					o.streamer.AskCommander(askInput.Question)
					return ChatResult{AskCommander: askInput.Question, Complete: false}, nil
				}
			}

			// Emit event with pre-injection params
			o.streamer.CallingTool(tc.ID, tc.Name, actionInput)

			// TODO(mission-issue): the error branches below feed the error back to
			// the LLM as a tool_result and let the agent decide what to do. That's
			// invisible to the command center. Emit a warning-severity mission_issue
			// (category=tool_error) here — non-fatal, no retry signal — so operators
			// can see tool-call churn without tailing debug logs.

			// Inject protected values before execution
			injectedInput, secretErr := o.secretInjector.Inject(actionInput)
			if secretErr != nil {
				errMsg := fmt.Sprintf("Error: %v", secretErr)
				o.streamer.ToolComplete(tc.ID, tc.Name, errMsg)
				toolResults = append(toolResults, llm.ToolResultBlock{
					ToolUseID: tc.ID,
					Content:   errMsg,
					IsError:   true,
				})
				continue
			}

			// Look up the tool
			tool := o.lookupTool(tc.Name)
			if tool == nil {
				errMsg := fmt.Sprintf("Error: Tool '%s' not found", tc.Name)
				o.streamer.ToolComplete(tc.ID, tc.Name, errMsg)
				toolResults = append(toolResults, llm.ToolResultBlock{
					ToolUseID: tc.ID,
					Content:   errMsg,
					IsError:   true,
				})
				continue
			}

			// Write-ahead: record tool call before execution
			var toolRecordID string
			if o.sessionLogger != nil && o.sessionID != "" {
				toolRecordID, _ = o.sessionLogger.StartToolCall(o.taskID, o.sessionID, tc.ID, tc.Name, injectedInput)
			}

			if o.eventLogger != nil {
				o.eventLogger.LogEvent("agent_tool_call", map[string]any{
					"tool": tc.Name,
				})
			}

			toolStart := time.Now()
			result := tool.Call(ctx, injectedInput)

			if o.eventLogger != nil {
				o.eventLogger.LogEvent("agent_tool_result", map[string]any{
					"tool":        tc.Name,
					"duration_ms": time.Since(toolStart).Milliseconds(),
				})
			}

			// Complete the tool call record with result
			if toolRecordID != "" && o.sessionLogger != nil {
				o.sessionLogger.CompleteToolCall(toolRecordID, result)
			} else if o.sessionLogger != nil && o.sessionID != "" {
				o.sessionLogger.StoreToolResult(o.taskID, o.sessionID, tc.ID, tc.Name, injectedInput, result, toolStart, time.Now())
			}

			// Apply result interception for large results
			resultContent := result
			if o.interceptor != nil {
				ir := o.interceptor.Intercept(tc.Name, result)
				resultContent = ir.Data
				if ir.Metadata != "" {
					resultContent += "\n\n---\n" + ir.Metadata
				}
			}

			o.streamer.ToolComplete(tc.ID, tc.Name, resultContent)

			toolResults = append(toolResults, llm.ToolResultBlock{
				ToolUseID: tc.ID,
				Content:   resultContent,
			})
		}

		// Add all tool results to the session
		o.session.AddToolResults(toolResults)

		// Log tool results to session store. We persist all results from this
		// turn as a single user message with one tool_result part per call —
		// matching how the providers see them and what we feed back on resume.
		if o.sessionLogger != nil && o.sessionID != "" && len(toolResults) > 0 {
			now := time.Now()
			parts := make([]llm.ContentBlock, 0, len(toolResults))
			for i := range toolResults {
				tr := toolResults[i]
				parts = append(parts, llm.ContentBlock{
					Type:       llm.ContentTypeToolResult,
					ToolResult: &tr,
				})
			}
			msg := llm.Message{Role: llm.RoleUser, Parts: parts}
			o.sessionLogger.AppendStructuredMessage(o.sessionID, "user", AuditContentForMessage(msg), PartsFromMessage(msg), now, now)
		}

		// Reset for next iteration
		currentParts = nil
	}

	return ChatResult{Answer: finalAnswer, Complete: finalAnswer != ""}, nil
}

// getSessionMessages retrieves the current message history from the underlying session.
func (o *orchestrator) getSessionMessages() []llm.Message {
	if adapter, ok := o.session.(*llm.SessionAdapter); ok {
		return adapter.GetSession().SnapshotMessages()
	}
	return nil
}


// checkAndCompact checks if compaction is needed and performs it if so
func (o *orchestrator) checkAndCompact(inputTokens int) {
	if o.compaction == nil || o.compaction.TokenLimit <= 0 {
		return
	}

	if inputTokens <= o.compaction.TokenLimit {
		return
	}

	// Get the underlying session to perform compaction
	adapter, ok := o.session.(*llm.SessionAdapter)
	if !ok {
		return
	}

	compacted := adapter.GetSession().Compact(o.compaction.TurnRetention)
	if compacted > 0 {
		if o.onCompaction != nil {
			o.onCompaction(inputTokens, o.compaction.TokenLimit, compacted, o.compaction.TurnRetention)
		}
		if o.eventLogger != nil {
			o.eventLogger.LogEvent("compaction", map[string]any{
				"input_tokens":       inputTokens,
				"token_limit":        o.compaction.TokenLimit,
				"messages_compacted": compacted,
				"turn_retention":     o.compaction.TurnRetention,
			})
		}
	}
}

// applyTurnPruning applies threshold-based pruning if configured
func (o *orchestrator) applyTurnPruning() {
	if o.pruningManager == nil {
		return
	}

	dropped := o.pruningManager.ApplyTurnPruning()
	if dropped > 0 && o.eventLogger != nil {
		o.eventLogger.LogEvent("turn_prune", map[string]any{
			"messages_dropped": dropped,
		})
	}
}


// lookupTool finds a tool by name
func (o *orchestrator) lookupTool(name string) aitools.Tool {
	if tool, ok := o.tools[name]; ok {
		return tool
	}
	return nil
}

