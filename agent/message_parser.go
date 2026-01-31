package agent

import (
	"strings"

	"squad/streamers"
)

// MessageParserState represents the current parsing state
type MessageParserState int

const (
	StateNone MessageParserState = iota
	StateReasoning
	StateAction
	StateActionInput
	StateAnswer
	StateAskSupe
)

// MessageParser parses ReAct-formatted streaming output and dispatches to a ChatHandler
type MessageParser struct {
	streamer          streamers.ChatHandler
	state             MessageParserState
	buffer            strings.Builder
	thinkingDisplayed bool
	reasoningStarted  bool
	answerStarted     bool
	askSupeStarted    bool
	actionName        string
	actionInput       string
	answerText        strings.Builder
	askSupeText       strings.Builder
}

// NewMessageParser creates a new parser with the given handler
// Immediately shows thinking indicator when created
func NewMessageParser(streamer streamers.ChatHandler) *MessageParser {
	streamer.Thinking()
	return &MessageParser{
		streamer:          streamer,
		state:             StateNone,
		thinkingDisplayed: true,
	}
}

// ProcessChunk processes an incoming chunk of streamed content
func (p *MessageParser) ProcessChunk(chunk string) {
	p.buffer.WriteString(chunk)
	p.processBuffer()
}

// GetAction returns the parsed action name (available after ACTION tag closes)
func (p *MessageParser) GetAction() string {
	return p.actionName
}

// GetActionInput returns the parsed action input (available after ACTION_INPUT tag closes)
func (p *MessageParser) GetActionInput() string {
	return p.actionInput
}

// GetAnswer returns the parsed answer text (available after ANSWER tag closes)
func (p *MessageParser) GetAnswer() string {
	return p.answerText.String()
}

// GetAskSupe returns the parsed ASK_SUPE text (available after ASK_SUPE tag closes)
func (p *MessageParser) GetAskSupe() string {
	return p.askSupeText.String()
}

// Finish signals that streaming is complete
// If we're in the middle of parsing ACTION_INPUT when the stream ends
// (due to stop sequences), capture the buffer content as the action input
func (p *MessageParser) Finish() {
	if p.state == StateActionInput {
		// Stream ended while parsing action input (likely due to stop sequence)
		// Capture whatever is in the buffer as the action input
		p.actionInput = strings.TrimSpace(p.buffer.String())
	}
	p.streamer.FinishAnswer()
}

// Reset resets the parser state for a new message
func (p *MessageParser) Reset() {
	p.state = StateNone
	p.buffer.Reset()
	p.thinkingDisplayed = false
	p.reasoningStarted = false
	p.answerStarted = false
	p.askSupeStarted = false
	p.actionName = ""
	p.actionInput = ""
	p.answerText.Reset()
	p.askSupeText.Reset()
}

func (p *MessageParser) processBuffer() {
	content := p.buffer.String()

	for {
		switch p.state {
		case StateNone:
			// Look for opening tags
			if idx := strings.Index(content, "<REASONING>"); idx != -1 {
				// Thinking is already displayed on parser creation
				p.state = StateReasoning
				content = content[idx+11:] // len("<REASONING>") = 11
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ACTION>"); idx != -1 {
				p.state = StateAction
				content = content[idx+8:] // len("<ACTION>") = 8
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ACTION_INPUT>"); idx != -1 {
				p.state = StateActionInput
				content = content[idx+14:] // len("<ACTION_INPUT>") = 14
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ANSWER>"); idx != -1 {
				p.state = StateAnswer
				content = content[idx+8:] // len("<ANSWER>") = 8
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ASK_SUPE>"); idx != -1 {
				p.state = StateAskSupe
				content = content[idx+10:] // len("<ASK_SUPE>") = 10
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return // No tags found, wait for more data

		case StateReasoning:
			// For REASONING, we stream content as it arrives
			// Strip leading newlines if this is the start of the reasoning
			if !p.reasoningStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.reasoningStarted = true
				}
			}

			if idx := strings.Index(content, "</REASONING>"); idx != -1 {
				// Emit remaining content before closing tag, trimming trailing newlines
				finalContent := strings.TrimRight(content[:idx], "\n")
				if len(finalContent) > 0 {
					p.streamer.PublishReasoningChunk(finalContent)
				}
				p.streamer.FinishReasoning()
				p.state = StateNone
				content = content[idx+12:] // len("</REASONING>") = 12
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			// No closing tag yet - emit what we have but keep some buffer
			// in case "</REASONING>" is split across chunks
			if len(content) > 12 {
				safeLen := len(content) - 12 // Keep last 12 chars in buffer
				p.streamer.PublishReasoningChunk(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return

		case StateAction:
			if idx := strings.Index(content, "</ACTION>"); idx != -1 {
				p.actionName = strings.TrimSpace(content[:idx])
				p.state = StateNone
				content = content[idx+9:] // len("</ACTION>") = 9
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return // Wait for closing tag

		case StateActionInput:
			if idx := strings.Index(content, "</ACTION_INPUT>"); idx != -1 {
				p.actionInput = strings.TrimSpace(content[:idx])
				p.state = StateNone
				content = content[idx+15:] // len("</ACTION_INPUT>") = 15
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return // Wait for closing tag

		case StateAnswer:
			// For ANSWER, we stream content as it arrives
			// Strip leading newlines if this is the start of the answer
			if !p.answerStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.answerStarted = true
				}
			}

			if idx := strings.Index(content, "</ANSWER>"); idx != -1 {
				// Emit remaining content before closing tag, trimming trailing newlines
				finalContent := strings.TrimRight(content[:idx], "\n")
				if len(finalContent) > 0 {
					p.streamer.PublishAnswerChunk(finalContent)
					p.answerText.WriteString(finalContent)
				}
				p.state = StateNone
				content = content[idx+9:] // len("</ANSWER>") = 9
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			// No closing tag yet - emit what we have but keep some buffer
			// in case "</ANSWER>" is split across chunks
			if len(content) > 9 {
				safeLen := len(content) - 9 // Keep last 9 chars in buffer
				p.streamer.PublishAnswerChunk(content[:safeLen])
				p.answerText.WriteString(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return

		case StateAskSupe:
			// For ASK_SUPE, we collect the full content (don't stream to user)
			// Strip leading newlines if this is the start
			if !p.askSupeStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.askSupeStarted = true
				}
			}

			if idx := strings.Index(content, "</ASK_SUPE>"); idx != -1 {
				// Capture content before closing tag, trimming trailing newlines
				finalContent := strings.TrimRight(content[:idx], "\n")
				p.askSupeText.WriteString(finalContent)
				p.state = StateNone
				content = content[idx+11:] // len("</ASK_SUPE>") = 11
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			// No closing tag yet - accumulate but keep buffer for split tag detection
			if len(content) > 11 {
				safeLen := len(content) - 11 // Keep last 11 chars in buffer
				p.askSupeText.WriteString(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return
		}
	}
}
