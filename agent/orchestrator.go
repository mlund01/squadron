package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
		// Pre-call budget check — refuse to start a new LLM turn once the mission or
		// this task has exhausted its token/dollar budget.
		if o.budget != nil {
			if err := o.budget.CheckBudget(); err != nil {
				o.streamer.Error(err)
				return ChatResult{}, err
			}
		}

		// Create parser for streaming text content (REASONING/ANSWER tags)
		parser := NewMessageParser(o.streamer)

		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_llm_start", nil)
		}
		llmStart := time.Now()

		var resp *llm.ChatResponse
		var err error

		// Callback for streaming chunks — routes text to parser, tool events to streamer
		onChunk := func(chunk llm.StreamChunk) {
			if chunk.Content != "" {
				parser.ProcessChunk(chunk.Content)
			}
			// Note: tool call start events are emitted later with full payload (line ~290)
			// Don't emit here to avoid duplicate events
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

			// Log user message to session store
			if o.sessionLogger != nil && o.sessionID != "" {
				now := time.Now()
				o.sessionLogger.AppendMessage(o.sessionID, "user", input, now, now)
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

		// Charge the turn against the shared budget. First breach aborts the task and
		// (via the tracker's cancel hook) brings down the whole mission.
		if o.budget != nil && resp != nil {
			turnTokens := resp.Usage.InputTokens + resp.Usage.OutputTokens +
				resp.Usage.CacheReadTokens + resp.Usage.CacheWriteTokens
			var turnCost float64
			if pricing := llm.GetPricing(o.modelName, o.pricingOverrides); pricing != nil {
				c := llm.ComputeTurnCost(pricing, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadTokens, resp.Usage.CacheWriteTokens)
				turnCost = c.TotalCost
			}
			if err := o.budget.RecordUsage(turnTokens, turnCost); err != nil {
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

		parser.Finish()

		if err != nil {
			o.streamer.Error(err)
			return ChatResult{}, err
		}

		// Log assistant response to session store (include tool calls in content)
		if o.sessionLogger != nil && o.sessionID != "" && resp != nil {
			logContent := resp.Content
			for _, block := range resp.ContentBlocks {
				if block.Type == llm.ContentTypeToolUse && block.ToolUse != nil {
					logContent += fmt.Sprintf("\n[tool_use: %s(%s)]", block.ToolUse.Name, string(block.ToolUse.Input))
				}
			}
			o.sessionLogger.AppendMessage(o.sessionID, "assistant", logContent, llmStart, time.Now())
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

		// Log tool results for session store
		if o.sessionLogger != nil && o.sessionID != "" {
			for _, tr := range toolResults {
				o.sessionLogger.AppendMessage(o.sessionID, "user", fmt.Sprintf("[tool_result:%s] %s", tr.ToolUseID, tr.Content), time.Now(), time.Now())
			}
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

