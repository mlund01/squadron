package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"squad/aitools"
	"squad/llm"
	"squad/streamers"
)

// llmSession defines the interface for LLM session operations needed by the orchestrator
type llmSession interface {
	// SendStream sends a message and streams the response, calling onChunk for each chunk
	SendStream(ctx context.Context, userMessage string, onChunk func(content string)) (*llm.ChatResponse, error)
	// SendMessageStream sends a multimodal message and streams the response
	SendMessageStream(ctx context.Context, msg llm.Message, onChunk func(content string)) (*llm.ChatResponse, error)
}

// pruneToolExclusions are internal helper tools whose results should never be pruned
var pruneToolExclusions = map[string]bool{
	"result_info":       true,
	"result_items":      true,
	"result_get":        true,
	"result_keys":       true,
	"result_to_dataset": true,
}

// pendingPrune holds pruning parameters from the last tool call
type pendingPrune struct {
	toolName             string
	overrideToolRecency  int // LLM override (0 = use default)
	overrideMsgRecency   int // LLM override (0 = use default)
}

// orchestrator handles the agent conversation loop
type orchestrator struct {
	session        llmSession
	streamer       streamers.ChatHandler
	tools          map[string]aitools.Tool
	interceptor    *aitools.ResultInterceptor
	pruningManager *llm.PruningManager
	pendingPrune   *pendingPrune
	eventLogger    EventLogger
	turnLogger     *llm.TurnLogger
}

// newOrchestrator creates a new chat orchestrator
func newOrchestrator(session llmSession, streamer streamers.ChatHandler, tools map[string]aitools.Tool, interceptor *aitools.ResultInterceptor, pruningManager *llm.PruningManager, eventLogger EventLogger, turnLogFile string) *orchestrator {
	o := &orchestrator{
		session:        session,
		streamer:       streamer,
		tools:          tools,
		interceptor:    interceptor,
		pruningManager: pruningManager,
		eventLogger:    eventLogger,
	}
	if turnLogFile != "" {
		if tl, err := llm.NewTurnLogger(turnLogFile); err == nil {
			o.turnLogger = tl
		}
	}
	return o
}

// processTurn handles a single conversation turn, including any tool calls
// Returns a ChatResult with either an answer (complete) or ASK_SUPE question (needs input)
func (o *orchestrator) processTurn(ctx context.Context, input string) (ChatResult, error) {
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
		if currentImageInput != nil {
			// Send image directly (not wrapped in OBSERVATION)
			msg := llm.NewImageMessage(llm.RoleUser, currentImageInput)
			resp, err = o.session.SendMessageStream(ctx, msg, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
			currentImageInput = nil // Reset for next iteration
		} else {
			resp, err = o.session.SendStream(ctx, currentTextInput, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
		}

		// Apply pending pruning from previous tool call AFTER the observation is in the session.
		// This registers the correct message (the one just sent) for future pruning.
		o.applyPendingPrune()

		if o.eventLogger != nil {
			eventData := map[string]any{
				"duration_ms": time.Since(llmStart).Milliseconds(),
			}
			if resp != nil {
				eventData["input_tokens"] = resp.Usage.InputTokens
				eventData["output_tokens"] = resp.Usage.OutputTokens
				// Include cache-related tokens if present
				if resp.Usage.CacheCreationInputTokens > 0 {
					eventData["cache_creation_input_tokens"] = resp.Usage.CacheCreationInputTokens
				}
				if resp.Usage.CacheReadInputTokens > 0 {
					eventData["cache_read_input_tokens"] = resp.Usage.CacheReadInputTokens
				}
				if resp.Usage.CachedTokens > 0 {
					eventData["cached_tokens"] = resp.Usage.CachedTokens
				}
			}
			o.eventLogger.LogEvent("agent_llm_end", eventData)
		}

		parser.Finish()

		if err != nil {
			o.streamer.Error(err)
			return ChatResult{}, err
		}

		// Determine what action (if any) was parsed — needed for turn logging
		action := parser.GetAction()

		// Log turn snapshot after LLM response is in the session
		if o.turnLogger != nil {
			o.turnLogger.LogTurn(action, o.getSessionMessages())
		}

		// Check for ASK_SUPE first (takes priority - agent needs supervisor input)
		if askSupe := parser.GetAskSupe(); askSupe != "" {
			return ChatResult{AskSupe: askSupe, Complete: false}, nil
		}

		// Capture the answer if one was provided
		if answer := parser.GetAnswer(); answer != "" {
			finalAnswer = answer
		}
		if action == "" {
			break // No tool call, done with this turn
		}

		actionInput := parser.GetActionInput()

		// Extract and strip pruning parameters from action input
		cleanInput, pruneParams := o.extractPruneParams(action, actionInput)

		o.streamer.CallingTool(action, cleanInput)

		// Look up the tool
		tool := o.lookupTool(action)
		if tool == nil {
			o.streamer.ToolComplete(action)
			currentTextInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found\n</OBSERVATION>", action)
			continue
		}

		// Execute the tool with cleaned input (pruning params stripped)
		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_tool_call", map[string]any{
				"tool": action,
			})
		}
		toolStart := time.Now()

		result := tool.Call(cleanInput)

		if o.eventLogger != nil {
			o.eventLogger.LogEvent("agent_tool_result", map[string]any{
				"tool":        action,
				"duration_ms": time.Since(toolStart).Milliseconds(),
			})
		}

		o.streamer.ToolComplete(action)

		// Store pending prune params - will be applied after next SendStream
		o.pendingPrune = pruneParams

		// Check if result is an image or format as observation
		currentTextInput, currentImageInput = o.formatObservation(action, result)
	}

	// Apply any remaining pending prune from the last tool call so it's not lost
	o.applyPendingPrune()

	if o.turnLogger != nil {
		o.turnLogger.Close()
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

// extractPruneParams extracts and strips pruning parameters from tool input JSON.
// For prunable tools, always returns a pendingPrune (with 0 overrides if LLM didn't specify).
// For excluded tools, returns nil pendingPrune.
func (o *orchestrator) extractPruneParams(toolName, actionInput string) (string, *pendingPrune) {
	if o.pruningManager == nil {
		return actionInput, nil
	}

	// Skip tools that should never be pruned
	if pruneToolExclusions[toolName] {
		return actionInput, nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(actionInput), &parsed); err != nil {
		// Not valid JSON - still register for pruning with defaults
		return actionInput, &pendingPrune{toolName: toolName}
	}

	// Extract pruning override parameters
	var toolRecency, msgRecency int
	changed := false
	if v, ok := parsed["tool_recency_limit"].(float64); ok {
		toolRecency = int(v)
		delete(parsed, "tool_recency_limit")
		changed = true
	}
	if v, ok := parsed["message_recency_limit"].(float64); ok {
		msgRecency = int(v)
		delete(parsed, "message_recency_limit")
		changed = true
	}

	// Re-serialize only if we stripped params
	cleanInput := actionInput
	if changed {
		if b, err := json.Marshal(parsed); err == nil {
			cleanInput = string(b)
		}
	}

	return cleanInput, &pendingPrune{
		toolName:            toolName,
		overrideToolRecency: toolRecency,
		overrideMsgRecency:  msgRecency,
	}
}

// applyPendingPrune applies any pending pruning from the previous tool call
func (o *orchestrator) applyPendingPrune() {
	if o.pendingPrune == nil || o.pruningManager == nil {
		return
	}

	o.pruningManager.RegisterAndPrune(
		o.pendingPrune.toolName,
		o.pendingPrune.overrideToolRecency,
		o.pendingPrune.overrideMsgRecency,
	)

	o.pendingPrune = nil
}

// lookupTool finds a tool by name
func (o *orchestrator) lookupTool(name string) aitools.Tool {
	if tool, ok := o.tools[name]; ok {
		return tool
	}
	return nil
}

// formatObservation formats a tool result as an observation, with optional metadata
// If the result is an image, returns empty string and the ImageBlock (images are not wrapped in OBSERVATION)
func (o *orchestrator) formatObservation(toolName, result string) (string, *llm.ImageBlock) {
	// Check if result is an image first
	if img := aitools.DetectImage(result); img != nil {
		return "", &llm.ImageBlock{
			Data:      img.Data,
			MediaType: img.MediaType,
		}
	}

	// Not an image - format as text observation
	if o.interceptor == nil {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", result), nil
	}

	ir := o.interceptor.Intercept(toolName, result)
	if ir.Metadata == "" {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", ir.Data), nil
	}

	return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>\n<OBSERVATION_METADATA>\n%s\n</OBSERVATION_METADATA>", ir.Data, ir.Metadata), nil
}
