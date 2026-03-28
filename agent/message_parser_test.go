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
func (m *mockStreamer) FinishAnswer() { m.finishAnswerCount++ }

func TestParseAction(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ACTION>my_tool</ACTION>")
	p.ProcessChunk("<ACTION_INPUT>{\"key\": \"value\"}</ACTION_INPUT>")
	p.Finish()

	if p.GetAction() != "my_tool" {
		t.Fatalf("action = %q, want %q", p.GetAction(), "my_tool")
	}
	if p.GetActionInput() != `{"key": "value"}` {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), `{"key": "value"}`)
	}
}

func TestParseAction_WhitespaceInName(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ACTION>  my_tool  </ACTION>")
	p.Finish()

	if p.GetAction() != "my_tool" {
		t.Fatalf("action = %q, want %q", p.GetAction(), "my_tool")
	}
}

func TestParseAction_WhitespaceInInput(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ACTION>tool</ACTION>")
	p.ProcessChunk("<ACTION_INPUT>  {\"a\":1}  </ACTION_INPUT>")
	p.Finish()

	if p.GetActionInput() != `{"a":1}` {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), `{"a":1}`)
	}
}

func TestParseActionInput_StreamEndBeforeClosingTag(t *testing.T) {
	// When stream ends during ACTION_INPUT (stop sequence), Finish() captures buffer
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ACTION>tool</ACTION>")
	p.ProcessChunk("<ACTION_INPUT>{\"partial\": true}")
	// No closing tag — stream ends
	p.Finish()

	if p.GetActionInput() != `{"partial": true}` {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), `{"partial": true}`)
	}
}

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

func TestParseReasoning(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<REASONING>Let me think about this step by step.</REASONING>")
	p.Finish()

	combined := strings.Join(s.reasoningChunks, "")
	if combined != "Let me think about this step by step." {
		t.Fatalf("reasoning = %q, want %q", combined, "Let me think about this step by step.")
	}
	if s.finishReasoningCount != 1 {
		t.Fatalf("finishReasoning called %d times, want 1", s.finishReasoningCount)
	}
}

func TestParseReasoning_LeadingNewlinesStripped(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<REASONING>\n\nThinking</REASONING>")
	p.Finish()

	combined := strings.Join(s.reasoningChunks, "")
	if combined != "Thinking" {
		t.Fatalf("reasoning = %q, want %q", combined, "Thinking")
	}
}

func TestParseReasoningThenAction(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<REASONING>I should use the search tool.</REASONING>")
	p.ProcessChunk("<ACTION>search</ACTION>")
	p.ProcessChunk("<ACTION_INPUT>{\"query\": \"test\"}</ACTION_INPUT>")
	p.Finish()

	if p.GetAction() != "search" {
		t.Fatalf("action = %q, want %q", p.GetAction(), "search")
	}
	if p.GetActionInput() != `{"query": "test"}` {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), `{"query": "test"}`)
	}
	if s.finishReasoningCount != 1 {
		t.Fatalf("finishReasoning called %d times, want 1", s.finishReasoningCount)
	}
}

func TestParseReasoningThenAnswer(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<REASONING>The answer is clear.</REASONING>")
	p.ProcessChunk("<ANSWER>42</ANSWER>")
	p.Finish()

	if p.GetAnswer() != "42" {
		t.Fatalf("answer = %q, want %q", p.GetAnswer(), "42")
	}
	if p.GetAction() != "" {
		t.Fatalf("action = %q, want empty", p.GetAction())
	}
}

func TestParseAskCommander(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("<ASK_COMMANDER>What credentials should I use?</ASK_COMMANDER>")
	p.Finish()

	got := p.GetAskCommander()
	expected := "What credentials should I use?"
	if got != expected {
		t.Fatalf("ask_commander = %q, want %q", got, expected)
	}
}

func TestChunkedStreaming_Action(t *testing.T) {
	// Simulate content arriving in small chunks
	s := &mockStreamer{}
	p := NewMessageParser(s)

	chunks := []string{"<ACT", "ION>", "my_", "tool", "</ACTION>", "<ACTION_", "INPUT>", `{"k":`, `"v"}`, "</ACTION_INPUT>"}
	for _, c := range chunks {
		p.ProcessChunk(c)
	}
	p.Finish()

	if p.GetAction() != "my_tool" {
		t.Fatalf("action = %q, want %q", p.GetAction(), "my_tool")
	}
	if p.GetActionInput() != `{"k":"v"}` {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), `{"k":"v"}`)
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

	p.ProcessChunk("<ACTION>tool1</ACTION>")
	p.ProcessChunk("<ACTION_INPUT>input1</ACTION_INPUT>")
	p.Finish()

	if p.GetAction() != "tool1" {
		t.Fatalf("before reset: action = %q", p.GetAction())
	}

	p.Reset()

	if p.GetAction() != "" {
		t.Fatalf("after reset: action = %q, want empty", p.GetAction())
	}
	if p.GetActionInput() != "" {
		t.Fatalf("after reset: action input = %q, want empty", p.GetActionInput())
	}
	if p.GetAnswer() != "" {
		t.Fatalf("after reset: answer = %q, want empty", p.GetAnswer())
	}
	if p.GetAskCommander() != "" {
		t.Fatalf("after reset: ask_commander = %q, want empty", p.GetAskCommander())
	}
}

func TestNoTags_NoAction(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("Just some plain text with no tags at all.")
	p.Finish()

	if p.GetAction() != "" {
		t.Fatalf("action = %q, want empty", p.GetAction())
	}
	if p.GetAnswer() != "" {
		t.Fatalf("answer = %q, want empty", p.GetAnswer())
	}
}

func TestTextBeforeTags_Ignored(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	p.ProcessChunk("Some preamble text\n<ACTION>tool</ACTION><ACTION_INPUT>data</ACTION_INPUT>")
	p.Finish()

	if p.GetAction() != "tool" {
		t.Fatalf("action = %q, want %q", p.GetAction(), "tool")
	}
	if p.GetActionInput() != "data" {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), "data")
	}
}

func TestMultilineActionInput(t *testing.T) {
	s := &mockStreamer{}
	p := NewMessageParser(s)

	input := `{
  "query": "test",
  "limit": 10
}`
	p.ProcessChunk("<ACTION>search</ACTION>")
	p.ProcessChunk("<ACTION_INPUT>" + input + "</ACTION_INPUT>")
	p.Finish()

	if p.GetActionInput() != input {
		t.Fatalf("action input = %q, want %q", p.GetActionInput(), input)
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
