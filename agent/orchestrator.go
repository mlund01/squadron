package agent

import (
	"context"
	"fmt"

	"squad/aitools"
	"squad/llm"
	"squad/streamers"
)

// llmSession defines the interface for LLM session operations needed by the orchestrator
type llmSession interface {
	// SendStream sends a message and streams the response, calling onChunk for each chunk
	SendStream(ctx context.Context, userMessage string, onChunk func(content string)) error
	// SendMessageStream sends a multimodal message and streams the response
	SendMessageStream(ctx context.Context, msg llm.Message, onChunk func(content string)) error
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
// Returns a ChatResult with either an answer (complete) or ASK_SUPE question (needs input)
func (o *orchestrator) processTurn(ctx context.Context, input string) (ChatResult, error) {
	currentTextInput := input
	var currentImageInput *llm.ImageBlock
	var finalAnswer string

	for {
		// Create parser for this message
		parser := NewMessageParser(o.streamer)

		var err error
		if currentImageInput != nil {
			// Send image directly (not wrapped in OBSERVATION)
			msg := llm.NewImageMessage(llm.RoleUser, currentImageInput)
			err = o.session.SendMessageStream(ctx, msg, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
			currentImageInput = nil // Reset for next iteration
		} else {
			err = o.session.SendStream(ctx, currentTextInput, func(content string) {
				if content != "" {
					parser.ProcessChunk(content)
				}
			})
		}

		parser.Finish()

		if err != nil {
			o.streamer.Error(err)
			return ChatResult{}, err
		}

		// Check for ASK_SUPE first (takes priority - agent needs supervisor input)
		if askSupe := parser.GetAskSupe(); askSupe != "" {
			return ChatResult{AskSupe: askSupe, Complete: false}, nil
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
			currentTextInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found\n</OBSERVATION>", action)
			continue
		}

		// Execute the tool
		result := tool.Call(actionInput)

		o.streamer.ToolComplete(action)

		// Check if result is an image or format as observation
		currentTextInput, currentImageInput = o.formatObservation(action, result)
	}

	return ChatResult{Answer: finalAnswer, Complete: finalAnswer != ""}, nil
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
