package agent

import (
	"strings"
	"testing"
)

// mockStreamer captures calls to the ChatHandler interface for test assertions
type mockStreamer struct {
	thinkingCalled       bool
	reasoningChunks      []string
	finishReasoningCount int
	answerChunks         []string
	finishAnswerCount    int
	toolCalls            []string
	toolResults          []string
	errors               []error
}

func (m *mockStreamer) Welcome(agentName, modelName string) {}
func (m *mockStreamer) AwaitClientAnswer() (string, error)  { return "", nil }
func (m *mockStreamer) Goodbye()                            {}
func (m *mockStreamer) Error(err error)                     { m.errors = append(m.errors, err) }
func (m *mockStreamer) Thinking()                           { m.thinkingCalled = true }
func (m *mockStreamer) CallingTool(toolCallId, name, payload string) { m.toolCalls = append(m.toolCalls, name) }
func (m *mockStreamer) ToolComplete(toolCallId, name, result string) {
	m.toolResults = append(m.toolResults, name)
}
func (m *mockStreamer) PublishReasoningChunk(chunk string) {
	m.reasoningChunks = append(m.reasoningChunks, chunk)
}
func (m *mockStreamer) ReasoningStarted()   {}
func (m *mockStreamer) ReasoningCompleted() { m.finishReasoningCount++ }
func (m *mockStreamer) PublishAnswerChunk(chunk string) {
	m.answerChunks = append(m.answerChunks, chunk)
}
func (m *mockStreamer) FinishAnswer()              { m.finishAnswerCount++ }
func (m *mockStreamer) AskCommander(content string)      {}
func (m *mockStreamer) CommanderResponse(content string) {}

func TestParseAnswer(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ANSWER>The final answer is 42.</ANSWER>")
	p.Finish()

	if p.GetAnswer() != "The final answer is 42." {
		t.Fatalf("answer = %q, want %q", p.GetAnswer(), "The final answer is 42.")
	}
	// Should have streamed answer chunks
	combined := strings.Join(s.answerChunks, "")
	if combined != "The final answer is 42." {
		t.Fatalf("streamed answer = %q, want %q", combined, "The final answer is 42.")
	}
}

func TestParseAnswer_LeadingNewlinesStripped(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ANSWER>\n\nHello</ANSWER>")
	p.Finish()

	if p.GetAnswer() != "Hello" {
		t.Fatalf("answer = %q, want %q", p.GetAnswer(), "Hello")
	}
}

func TestParseAnswer_TrailingNewlinesStripped(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ANSWER>Hello\n\n</ANSWER>")
	p.Finish()

	if p.GetAnswer() != "Hello" {
		t.Fatalf("answer = %q, want %q", p.GetAnswer(), "Hello")
	}
}

// Reasoning is no longer parsed from text — it streams as separate
// StreamChunk fields from native-thinking providers. The MessageParser only
// handles ANSWER tags now. See agent/orchestrator.go onChunk for the
// reasoning event wiring.

func TestParser_IgnoresReasoningTags(t *testing.T) {
	// Verify that legacy <REASONING>...</REASONING> tags from old transcripts
	// don't crash the parser. They're treated as plain text and silently
	// dropped (no answer chunks emitted because no <ANSWER> tag present).
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<REASONING>old text</REASONING><ANSWER>42</ANSWER>")
	p.Finish()

	if p.GetAnswer() != "42" {
		t.Fatalf("answer = %q, want %q", p.GetAnswer(), "42")
	}
	if len(s.reasoningChunks) != 0 {
		t.Fatalf("reasoning chunks emitted from legacy tags: %v", s.reasoningChunks)
	}
}

func TestChunkedStreaming_Answer(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	chunks := []string{"<ANS", "WER>", "Hello ", "world", "</ANSWER>"}
	for _, c := range chunks {
		p.ProcessChunk(c)
	}
	p.Finish()

	if p.GetAnswer() != "Hello world" {
		t.Fatalf("answer = %q, want %q", p.GetAnswer(), "Hello world")
	}
}

func TestNewMessageParser_CallsThinking(t *testing.T) {
	s := &mockStreamer{}
	_ = NewMessageParser(s)

	if !s.thinkingCalled {
		t.Fatal("Thinking() not called on parser creation")
	}
}

func TestFinish_CallsFinishAnswer(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)
	p.Finish()

	if s.finishAnswerCount != 1 {
		t.Fatalf("FinishAnswer called %d times, want 1", s.finishAnswerCount)
	}
}

func TestReset(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ANSWER>hello</ANSWER>")
	p.Finish()

	if p.GetAnswer() != "hello" {
		t.Fatalf("before reset: answer = %q", p.GetAnswer())
	}

	p.Reset()

	if p.GetAnswer() != "" {
		t.Fatalf("after reset: answer = %q, want empty", p.GetAnswer())
	}
}

func TestNoTags_NoAnswer(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("Just some plain text with no tags at all.")
	p.Finish()

	if p.GetAnswer() != "" {
		t.Fatalf("answer = %q, want empty", p.GetAnswer())
	}
}

func TestLongAnswerStreaming(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	// Build a long answer that exceeds the 9-char buffer safety margin
	longText := strings.Repeat("word ", 100)
	p.ProcessChunk("<ANSWER>" + longText + "</ANSWER>")
	p.Finish()

	got := p.GetAnswer()
	expected := strings.TrimRight(longText, " ") // trailing space before </ANSWER> is preserved in content
	// The answer should contain the full text (modulo whitespace at boundaries)
	if !strings.Contains(got, "word") {
		t.Fatalf("answer missing expected content, got %q", got)
	}
	if len(got) < len(expected)-1 {
		t.Fatalf("answer too short: %d chars, expected ~%d", len(got), len(expected))
	}
}
