package agent

import (
	"strings"

	"squadron/streamers"
)

// MessageParserState represents the current parsing state
type MessageParserState int

const (
	StateNone MessageParserState = iota
	StateAnswer
)

// MessageParser parses streaming text output for ANSWER XML tags. Reasoning
// is no longer parsed from text — providers that expose native reasoning
// (Anthropic, Gemini) emit it as separate StreamChunk fields, and providers
// that don't simply skip reasoning altogether. Tool calls are handled via
// native SDK tool calling (not parsed from text).
type MessageParser struct {
	streamer          streamers.ChatHandler
	state             MessageParserState
	buffer            strings.Builder
	thinkingDisplayed bool
	answerStarted     bool
	answerText        strings.Builder
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

// GetAnswer returns the parsed answer text (available after ANSWER tag closes)
func (p *MessageParser) GetAnswer() string {
	return p.answerText.String()
}

// ProcessChunk processes an incoming chunk of streamed text content
func (p *MessageParser) ProcessChunk(chunk string) {
	p.buffer.WriteString(chunk)
	p.processBuffer()
}

// Finish signals that streaming is complete
func (p *MessageParser) Finish() {
	p.streamer.FinishAnswer()
}

// Reset resets the parser state for a new message
func (p *MessageParser) Reset() {
	p.state = StateNone
	p.buffer.Reset()
	p.thinkingDisplayed = false
	p.answerStarted = false
	p.answerText.Reset()
}

func (p *MessageParser) processBuffer() {
	content := p.buffer.String()

	for {
		switch p.state {
		case StateNone:
			// Look for opening ANSWER tag
			if idx := strings.Index(content, "<ANSWER>"); idx != -1 {
				p.state = StateAnswer
				content = content[idx+8:] // len("<ANSWER>") = 8
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return // No tags found, wait for more data

		case StateAnswer:
			// Stream answer content as it arrives
			if !p.answerStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.answerStarted = true
				}
			}

			if idx := strings.Index(content, "</ANSWER>"); idx != -1 {
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
			// No closing tag yet - emit what we have but keep buffer for split tag detection
			if len(content) > 9 {
				safeLen := len(content) - 9
				p.streamer.PublishAnswerChunk(content[:safeLen])
				p.answerText.WriteString(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return
		}
	}
}
