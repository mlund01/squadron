package agent

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
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
	SendStream(ctx context.Context, userMessage string, onChunk func(content string)) (*llm.ChatResponse, error)
	// SendMessageStream sends a multimodal message and streams the response
	SendMessageStream(ctx context.Context, msg llm.Message, onChunk func(content string)) (*llm.ChatResponse, error)
	// ContinueStream resumes from existing session state without adding a new user message
	ContinueStream(ctx context.Context, onChunk func(content string)) (*llm.ChatResponse, error)
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
	sessionLogger  SessionLogger
	sessionID      string
	taskID         string
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
	currentTextInput := input
	var currentImageInput *llm.ImageBlock
	var finalAnswer string

	for {
		// Create parser for this message
		parser := NewMessageParser(o.streamer)

		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_llm_start", nil)
		}
		llmStart := time.Now()

		var resp *llm.ChatResponse
		var err error

		if resume {
			// First turn of resume — LLM responds to existing state.
			// Don't add a new user message (the pending one is already in the session).
			resp, err = o.session.ContinueStream(ctx, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
			resume = false
		} else if currentImageInput != nil {
			// Send image directly (not wrapped in OBSERVATION)
			msg := llm.NewImageMessage(llm.RoleUser, currentImageInput)
			resp, err = o.session.SendMessageStream(ctx, msg, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
			currentImageInput = nil // Reset for next iteration
		} else {
			// Log user message to session store
			if o.sessionLogger != nil && o.sessionID != "" {
				now := time.Now()
				o.sessionLogger.AppendMessage(o.sessionID, "user", currentTextInput, now, now)
			}
			resp, err = o.session.SendStream(ctx, currentTextInput, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
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
				o.onSessionTurn(protocol.SessionTurnData{
					Model:                     o.modelName,
					InputTokens:              resp.Usage.InputTokens,
					OutputTokens:             resp.Usage.OutputTokens,
					CacheWriteTokens: resp.Usage.CacheWriteTokens,
					CacheReadTokens:  resp.Usage.CacheReadTokens,
					UserMessages:             stats.UserCount,
					AssistantMessages:        stats.AssistantCount,
					SystemMessages:           stats.SystemCount,
					PayloadBytes:             stats.PayloadBytes,
					TurnDurationMs:           time.Since(llmStart).Milliseconds(),
				})
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

		// Log assistant response to session store
		if o.sessionLogger != nil && o.sessionID != "" && resp != nil {
			o.sessionLogger.AppendMessage(o.sessionID, "assistant", resp.Content, llmStart, time.Now())
		}

		// Determine what action (if any) was parsed — needed for turn logging
		action := parser.GetAction()

		// Log turn snapshot after LLM response is in the session
		if o.turnLogger != nil {
			o.turnLogger.LogTurn(action, o.getSessionMessages())
		}

		// Check for ASK_COMMANDER first (takes priority - agent needs commander input)
		if askCommander := parser.GetAskCommander(); askCommander != "" {
			return ChatResult{AskCommander: askCommander, Complete: false}, nil
		}

		// Capture the answer if one was provided
		if answer := parser.GetAnswer(); answer != "" {
			finalAnswer = answer
		}
		if action == "" {
			break // No tool call, done with this turn
		}

		actionInput := parser.GetActionInput()

		// Log with placeholder version (secrets not exposed in logs)
		tcID := uuid.New().String()
		o.streamer.CallingTool(tcID, action, actionInput)

		// Inject secrets before tool execution
		injectedInput, secretErr := o.secretInjector.Inject(actionInput)
		if secretErr != nil {
			errMsg := fmt.Sprintf("Error: %v", secretErr)
			o.streamer.ToolComplete(tcID, action, errMsg)
			currentTextInput = fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", errMsg)
			continue
		}

		// Look up the tool
		tool := o.lookupTool(action)
		if tool == nil {
			errMsg := fmt.Sprintf("Error: Tool '%s' not found", action)
			o.streamer.ToolComplete(tcID, action, errMsg)
			currentTextInput = fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", errMsg)
			continue
		}

		// Execute the tool with injected secrets
		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_tool_call", map[string]any{
				"tool": action,
			})
		}
		toolStart := time.Now()

		result := tool.Call(injectedInput)

		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_tool_result", map[string]any{
				"tool":        action,
				"duration_ms": time.Since(toolStart).Milliseconds(),
			})
		}

		// Persist tool result for auditing
		if o.sessionLogger != nil && o.sessionID != "" {
			o.sessionLogger.StoreToolResult(o.taskID, o.sessionID, tcID, action, actionInput, result, toolStart, time.Now())
		}

		// Format observation (may intercept/truncate large results)
		var observationContent string
		currentTextInput, currentImageInput, observationContent = o.formatObservation(action, result)
		o.streamer.ToolComplete(tcID, action, observationContent)

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

// formatObservation formats a tool result as an observation, with optional metadata.
// Returns the formatted string, optional ImageBlock (for image results), and the observation content fed to the LLM.
func (o *orchestrator) formatObservation(toolName, result string) (string, *llm.ImageBlock, string) {
	// Check if result is an image first
	if img := aitools.DetectImage(result); img != nil {
		return "", &llm.ImageBlock{
			Data:      img.Data,
			MediaType: img.MediaType,
		}, ""
	}

	// Not an image - format as text observation
	if o.interceptor == nil {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", result), nil, result
	}

	ir := o.interceptor.Intercept(toolName, result)
	if ir.Metadata == "" {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", ir.Data), nil, ir.Data
	}

	return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>\n<OBSERVATION_METADATA>\n%s\n</OBSERVATION_METADATA>", ir.Data, ir.Metadata), nil, ir.Data
}
