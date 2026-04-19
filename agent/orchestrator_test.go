package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"squadron/aitools"
	"squadron/llm"
)

// fakeSession implements llmSession for testing. Each call to Send/Continue
// pops the next canned response from `responses` and appends a record of the
// call to `calls` so tests can assert ordering.
type fakeSession struct {
	responses   []*llm.ChatResponse
	errs        []error
	calls       []string
	toolResults [][]llm.ToolResultBlock
}

func (f *fakeSession) nextResp(onChunk func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	if len(f.responses) == 0 {
		return nil, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	var err error
	if len(f.errs) > 0 {
		err = f.errs[0]
		f.errs = f.errs[1:]
	}
	// Stream any text content through onChunk so the message parser can see it
	// (this is how <ANSWER> tags get surfaced in a real session).
	if resp != nil && onChunk != nil {
		for _, block := range resp.ContentBlocks {
			if block.Type == llm.ContentTypeText && block.Text != "" {
				onChunk(llm.StreamChunk{Content: block.Text})
			}
		}
	}
	return resp, err
}

func (f *fakeSession) SendStream(_ context.Context, _ string, onChunk func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	f.calls = append(f.calls, "send")
	return f.nextResp(onChunk)
}

func (f *fakeSession) SendMessageStream(_ context.Context, _ llm.Message, onChunk func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	f.calls = append(f.calls, "send_msg")
	return f.nextResp(onChunk)
}

func (f *fakeSession) ContinueStream(_ context.Context, onChunk func(llm.StreamChunk)) (*llm.ChatResponse, error) {
	f.calls = append(f.calls, "continue")
	return f.nextResp(onChunk)
}

func (f *fakeSession) AddToolResults(results []llm.ToolResultBlock) {
	f.toolResults = append(f.toolResults, results)
}

// newTestOrchestrator builds a minimal orchestrator wired to the fake session
// and a mock streamer — all optional collaborators (loggers, compaction,
// pruning, budgets) are left nil because the code paths under test guard on
// them.
func newTestOrchestrator(session llmSession, streamer *mockStreamer) *orchestrator {
	return &orchestrator{
		session:        session,
		streamer:       streamer,
		tools:          map[string]aitools.Tool{},
		secretInjector: newSecretInjector(nil),
	}
}

func textResponse(text, finish string) *llm.ChatResponse {
	return &llm.ChatResponse{
		Content: text,
		ContentBlocks: []llm.ContentBlock{
			{Type: llm.ContentTypeText, Text: text},
		},
		FinishReason: finish,
	}
}

func toolUseResponse(id, name, input, finish string) *llm.ChatResponse {
	return &llm.ChatResponse{
		ContentBlocks: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
				ID:    id,
				Name:  name,
				Input: json.RawMessage(input),
			}},
		},
		FinishReason: finish,
	}
}

func TestOrchestrator_MaxTokensRecoversAfterCorrection(t *testing.T) {
	// Two consecutive max_tokens hits, then a clean answer. The orchestrator
	// should emit corrections and finish without error.
	session := &fakeSession{
		responses: []*llm.ChatResponse{
			textResponse("partial reasoning...", "max_tokens"),
			textResponse("more partial...", "max_tokens"),
			textResponse("<ANSWER>done</ANSWER>", "end_turn"),
		},
	}
	streamer := &mockStreamer{}
	o := newTestOrchestrator(session, streamer)

	result, err := o.processTurn(context.Background(), "go", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Complete || result.Answer != "done" {
		t.Fatalf("expected complete answer=done, got %+v", result)
	}
	// Initial send + 2 corrections = 3 SendStream calls (all via "send"
	// because firstTurn uses SendStream and corrections use SendStream).
	sendCount := 0
	for _, c := range session.calls {
		if c == "send" {
			sendCount++
		}
	}
	if sendCount != 3 {
		t.Fatalf("expected 3 send calls (1 initial + 2 corrections), got %d (%v)", sendCount, session.calls)
	}
	if o.maxTokensRetries != 0 {
		t.Fatalf("expected maxTokensRetries reset to 0, got %d", o.maxTokensRetries)
	}
}

func TestOrchestrator_MaxTokensFailsAfterThreeAttempts(t *testing.T) {
	// Four consecutive max_tokens hits — should fail on the 4th attempt
	// (1 original + 3 corrections exhausted).
	session := &fakeSession{
		responses: []*llm.ChatResponse{
			textResponse("p1", "max_tokens"),
			textResponse("p2", "max_tokens"),
			textResponse("p3", "max_tokens"),
			textResponse("p4", "max_tokens"),
		},
	}
	streamer := &mockStreamer{}
	o := newTestOrchestrator(session, streamer)

	_, err := o.processTurn(context.Background(), "go", false)
	if err == nil {
		t.Fatalf("expected error after 3 failed corrections, got nil")
	}
	if !strings.Contains(err.Error(), "max output tokens after 3 correction attempts") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if o.maxTokensRetries != 4 {
		t.Fatalf("expected maxTokensRetries=4 (incremented past limit), got %d", o.maxTokensRetries)
	}
}

func TestOrchestrator_MaxTokensWithPartialToolUseEmitsErrorResults(t *testing.T) {
	// Truncation mid-tool-use: the orchestrator must emit a synthetic error
	// tool_result for the partial tool_use to satisfy Anthropic's protocol,
	// then send the correction. We assert AddToolResults was called with an
	// IsError result for the truncated tool call.
	session := &fakeSession{
		responses: []*llm.ChatResponse{
			toolUseResponse("tu-1", "some_tool", `{"incomplete":`, "max_tokens"),
			textResponse("<ANSWER>recovered</ANSWER>", "end_turn"),
		},
	}
	streamer := &mockStreamer{}
	o := newTestOrchestrator(session, streamer)

	result, err := o.processTurn(context.Background(), "go", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Complete {
		t.Fatalf("expected completion, got %+v", result)
	}
	if len(session.toolResults) == 0 {
		t.Fatalf("expected AddToolResults to be called with synthetic error result")
	}
	first := session.toolResults[0]
	if len(first) != 1 {
		t.Fatalf("expected 1 synthetic tool_result, got %d", len(first))
	}
	if first[0].ToolUseID != "tu-1" || !first[0].IsError {
		t.Fatalf("expected IsError result for tu-1, got %+v", first[0])
	}
	if !strings.Contains(first[0].Content, "truncated") {
		t.Fatalf("expected content to mention truncation, got %q", first[0].Content)
	}
}

func TestOrchestrator_MaxTokensCounterResetsOnSuccess(t *testing.T) {
	// A max_tokens hit followed by a clean turn should reset the counter, so
	// a subsequent max_tokens streak starts fresh with 3 more attempts.
	session := &fakeSession{
		responses: []*llm.ChatResponse{
			textResponse("trunc", "max_tokens"),
			textResponse("<ANSWER>ok</ANSWER>", "end_turn"),
		},
	}
	streamer := &mockStreamer{}
	o := newTestOrchestrator(session, streamer)

	if _, err := o.processTurn(context.Background(), "go", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.maxTokensRetries != 0 {
		t.Fatalf("expected counter reset to 0 after successful recovery, got %d", o.maxTokensRetries)
	}
}
