package agent

import (
	"context"
	"fmt"

	"squad/aitools"
	"squad/streamers"
)

// llmSession defines the interface for LLM session operations needed by the orchestrator
type llmSession interface {
	// SendStream sends a message and streams the response, calling onChunk for each chunk
	SendStream(ctx context.Context, userMessage string, onChunk func(content string)) error
}

// orchestrator handles the agent conversation loop
type orchestrator struct {
	session     llmSession
	streamer    streamers.ChatHandler
	tools       map[string]aitools.Tool
	interceptor *aitools.ResultInterceptor
}

// newOrchestrator creates a new chat orchestrator
func newOrchestrator(session llmSession, streamer streamers.ChatHandler, tools map[string]aitools.Tool, interceptor *aitools.ResultInterceptor) *orchestrator {
	return &orchestrator{
		session:     session,
		streamer:    streamer,
		tools:       tools,
		interceptor: interceptor,
	}
}

// processTurn handles a single conversation turn, including any tool calls
// Returns the final answer text (if any) and any error
func (o *orchestrator) processTurn(ctx context.Context, input string) (string, error) {
	currentInput := input
	var finalAnswer string

	for {
		// Create parser for this message
		parser := NewMessageParser(o.streamer)

		err := o.session.SendStream(ctx, currentInput, func(content string) {
			if content != "" {
				parser.ProcessChunk(content)
			}
		})

		parser.Finish()

		if err != nil {
			o.streamer.Error(err)
			return "", err
		}

		// Capture the answer if one was provided
		if answer := parser.GetAnswer(); answer != "" {
			finalAnswer = answer
		}

		// Check if there's an action to call
		action := parser.GetAction()
		if action == "" {
			break // No tool call, done with this turn
		}

		actionInput := parser.GetActionInput()
		o.streamer.CallingTool(action, actionInput)

		// Look up the tool
		tool := o.lookupTool(action)
		if tool == nil {
			o.streamer.ToolComplete(action)
			currentInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found\n</OBSERVATION>", action)
			continue
		}

		// Execute the tool
		result := tool.Call(actionInput)

		o.streamer.ToolComplete(action)

		// Intercept large results and format observation
		currentInput = o.formatObservation(action, result)
	}

	return finalAnswer, nil
}

// lookupTool finds a tool by name
func (o *orchestrator) lookupTool(name string) aitools.Tool {
	if tool, ok := o.tools[name]; ok {
		return tool
	}
	return nil
}

// formatObservation formats a tool result as an observation, with optional metadata
func (o *orchestrator) formatObservation(toolName, result string) string {
	if o.interceptor == nil {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", result)
	}

	ir := o.interceptor.Intercept(toolName, result)
	if ir.Metadata == "" {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", ir.Data)
	}

	return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>\n<OBSERVATION_METADATA>\n%s\n</OBSERVATION_METADATA>", ir.Data, ir.Metadata)
}
