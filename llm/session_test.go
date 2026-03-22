package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// failingProvider returns errors for the first N calls, then succeeds.
type failingProvider struct {
	failCount    int
	callCount    int
	statusCode   int
	lastResponse *ChatResponse
}

func (p *failingProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *failingProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	p.callCount++
	if p.callCount <= p.failCount {
		return nil, fmt.Errorf("POST \"https://api.example.com/v1/chat\": %d %s",
			p.statusCode, statusText(p.statusCode))
	}
	ch := make(chan StreamChunk, 2)
	ch <- StreamChunk{Content: "ok", Done: false}
	ch <- StreamChunk{Done: true, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}
	close(ch)
	return ch, nil
}

func statusText(code int) string {
	switch code {
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 503:
		return "Service Unavailable"
	default:
		return "Error"
	}
}

func TestRetry_429Recovery(t *testing.T) {
	provider := &failingProvider{failCount: 2, statusCode: 429}
	session := NewSession(provider, "test-model", "system prompt")

	resp, err := session.SendStream(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected content 'ok', got %q", resp.Content)
	}
	if provider.callCount != 3 {
		t.Fatalf("expected 3 calls (2 failures + 1 success), got %d", provider.callCount)
	}
}

func TestRetry_500Recovery(t *testing.T) {
	provider := &failingProvider{failCount: 1, statusCode: 500}
	session := NewSession(provider, "test-model", "system prompt")

	resp, err := session.SendStream(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected content 'ok', got %q", resp.Content)
	}
	if provider.callCount != 2 {
		t.Fatalf("expected 2 calls (1 failure + 1 success), got %d", provider.callCount)
	}
}

func TestRetry_NonRetryableError(t *testing.T) {
	provider := &failingProvider{failCount: 100, statusCode: 400}
	session := NewSession(provider, "test-model", "system prompt")

	_, err := session.SendStream(context.Background(), "hello", nil)
	if err == nil {
		t.Fatal("expected error for non-retryable status")
	}
	if provider.callCount != 1 {
		t.Fatalf("expected 1 call (no retry for 400), got %d", provider.callCount)
	}
}

func TestRetry_ContextCancellation(t *testing.T) {
	provider := &failingProvider{failCount: 100, statusCode: 429}
	session := NewSession(provider, "test-model", "system prompt")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := session.SendStream(ctx, "hello", nil)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if provider.callCount < 1 {
		t.Fatal("expected at least 1 call")
	}
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	provider := &failingProvider{failCount: 3, statusCode: 503}
	session := NewSession(provider, "test-model", "system prompt")

	start := time.Now()
	resp, err := session.SendStream(context.Background(), "hello", nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected content 'ok', got %q", resp.Content)
	}
	// 3 retries: 5s + 10s + 20s = 35s minimum
	if elapsed < 30*time.Second {
		t.Fatalf("expected at least 30s of backoff, got %s", elapsed)
	}
	if provider.callCount != 4 {
		t.Fatalf("expected 4 calls (3 failures + 1 success), got %d", provider.callCount)
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		msg       string
		retryable bool
	}{
		{`POST "https://api.openai.com/v1/chat": 429 Too Many Requests`, true},
		{`POST "https://api.openai.com/v1/chat": 500 Internal Server Error`, true},
		{`POST "https://api.openai.com/v1/chat": 502 Bad Gateway`, true},
		{`POST "https://api.openai.com/v1/chat": 503 Service Unavailable`, true},
		{`POST "https://api.openai.com/v1/chat": 504 Gateway Timeout`, true},
		{`POST "https://api.anthropic.com/v1/messages": 529 Overloaded`, true},
		{`POST "https://api.openai.com/v1/chat": 400 Bad Request`, false},
		{`POST "https://api.openai.com/v1/chat": 401 Unauthorized`, false},
		{`POST "https://api.openai.com/v1/chat": 403 Forbidden`, false},
		{`connection refused`, false},
	}

	for _, tt := range tests {
		err := fmt.Errorf("%s", tt.msg)
		got := isRetryableError(err)
		if got != tt.retryable {
			t.Errorf("isRetryableError(%q) = %v, want %v", tt.msg, got, tt.retryable)
		}
	}
}

// ---------------------------------------------------------------------------
// mockProvider — configurable provider for non-retry tests
// ---------------------------------------------------------------------------

// mockProvider returns configurable responses for Chat and ChatStream.
type mockProvider struct {
	chatResponse   *ChatResponse
	chatErr        error
	streamChunks   []StreamChunk
	streamErr      error
	lastRequest    *ChatRequest // captured from most recent call
	chatCallCount  int
}

func (p *mockProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	p.chatCallCount++
	p.lastRequest = req
	if p.chatErr != nil {
		return nil, p.chatErr
	}
	return p.chatResponse, nil
}

func (p *mockProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	p.lastRequest = req
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	ch := make(chan StreamChunk, len(p.streamChunks))
	for _, c := range p.streamChunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// newMockProvider creates a mockProvider that streams the given content as a single response.
func newMockProvider(content string) *mockProvider {
	return &mockProvider{
		chatResponse: &ChatResponse{ID: "resp-1", Content: content},
		streamChunks: []StreamChunk{
			{Content: content, Done: false},
			{Done: true, Usage: &Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}
}

// ---------------------------------------------------------------------------
// 1. Session creation
// ---------------------------------------------------------------------------

func TestNewSession_ZeroSystemPrompts(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	if len(s.systemPrompts) != 0 {
		t.Fatalf("expected 0 system prompts, got %d", len(s.systemPrompts))
	}
	if len(s.messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(s.messages))
	}
	if s.model != "m" {
		t.Fatalf("expected model 'm', got %q", s.model)
	}
}

func TestNewSession_OneSystemPrompt(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "you are helpful")
	if len(s.systemPrompts) != 1 {
		t.Fatalf("expected 1 system prompt, got %d", len(s.systemPrompts))
	}
	if s.systemPrompts[0] != "you are helpful" {
		t.Fatalf("unexpected prompt: %q", s.systemPrompts[0])
	}
}

func TestNewSession_MultipleSystemPrompts(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "prompt-a", "prompt-b", "prompt-c")
	if len(s.systemPrompts) != 3 {
		t.Fatalf("expected 3 system prompts, got %d", len(s.systemPrompts))
	}
	want := []string{"prompt-a", "prompt-b", "prompt-c"}
	for i, w := range want {
		if s.systemPrompts[i] != w {
			t.Fatalf("systemPrompts[%d] = %q, want %q", i, s.systemPrompts[i], w)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Message history
// ---------------------------------------------------------------------------

func TestGetHistory_AfterSend(t *testing.T) {
	p := newMockProvider("response-1")
	s := NewSession(p, "m", "sys")

	_, err := s.Send(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	p.chatResponse = &ChatResponse{ID: "resp-2", Content: "response-2"}
	_, err = s.Send(context.Background(), "world")
	if err != nil {
		t.Fatal(err)
	}

	history := s.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected 4 messages in history, got %d", len(history))
	}

	expected := []struct {
		role    Role
		content string
	}{
		{RoleUser, "hello"},
		{RoleAssistant, "response-1"},
		{RoleUser, "world"},
		{RoleAssistant, "response-2"},
	}
	for i, e := range expected {
		if history[i].Role != e.role || history[i].Content != e.content {
			t.Errorf("history[%d] = {%s, %q}, want {%s, %q}",
				i, history[i].Role, history[i].Content, e.role, e.content)
		}
	}
}

func TestGetHistory_AfterSendStream(t *testing.T) {
	p := newMockProvider("streamed-reply")
	s := NewSession(p, "m")

	var chunks []string
	_, err := s.SendStream(context.Background(), "ping", func(c StreamChunk) {
		chunks = append(chunks, c.Content)
	})
	if err != nil {
		t.Fatal(err)
	}

	history := s.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != RoleUser || history[0].Content != "ping" {
		t.Errorf("unexpected user message: %+v", history[0])
	}
	if history[1].Role != RoleAssistant || history[1].Content != "streamed-reply" {
		t.Errorf("unexpected assistant message: %+v", history[1])
	}
}

// ---------------------------------------------------------------------------
// 3. Clone — deep copy isolation
// ---------------------------------------------------------------------------

func TestClone_DeepCopyIsolation(t *testing.T) {
	p := newMockProvider("resp")
	s := NewSession(p, "m", "sys-prompt")
	s.SetStopSequences([]string{"STOP"})

	// Add a message with Parts and Metadata
	s.messages = append(s.messages, Message{
		Role:    RoleUser,
		Content: "text-msg",
		Parts: []ContentBlock{
			{Type: ContentTypeText, Text: "part-text"},
			{Type: ContentTypeImage, ImageData: &ImageBlock{Data: "abc", MediaType: "image/png"}},
		},
		Metadata: &MessageMetadata{MessageID: "id-1", ToolName: "tool-1", MessageIndex: 0},
	})
	s.messages = append(s.messages, Message{Role: RoleAssistant, Content: "answer"})

	clone := s.Clone()

	// Verify values are equal
	if clone.model != s.model {
		t.Fatalf("model mismatch")
	}
	if len(clone.systemPrompts) != len(s.systemPrompts) {
		t.Fatalf("system prompt count mismatch")
	}
	if len(clone.messages) != len(s.messages) {
		t.Fatalf("message count mismatch")
	}
	if len(clone.stopSequences) != len(s.stopSequences) {
		t.Fatalf("stop sequence count mismatch")
	}

	// Mutate the clone and verify original is unaffected
	clone.systemPrompts[0] = "mutated"
	if s.systemPrompts[0] == "mutated" {
		t.Fatal("clone mutation affected original systemPrompts")
	}

	clone.messages[0].Content = "mutated"
	if s.messages[0].Content == "mutated" {
		t.Fatal("clone mutation affected original messages Content")
	}

	clone.messages[0].Parts[0].Text = "mutated"
	if s.messages[0].Parts[0].Text == "mutated" {
		t.Fatal("clone mutation affected original Parts text")
	}

	clone.messages[0].Parts[1].ImageData.Data = "mutated"
	if s.messages[0].Parts[1].ImageData.Data == "mutated" {
		t.Fatal("clone mutation affected original ImageData")
	}

	clone.messages[0].Metadata.MessageID = "mutated"
	if s.messages[0].Metadata.MessageID == "mutated" {
		t.Fatal("clone mutation affected original Metadata")
	}

	clone.stopSequences[0] = "mutated"
	if s.stopSequences[0] == "mutated" {
		t.Fatal("clone mutation affected original stopSequences")
	}

	// Debug file should not be shared
	if clone.debugFile != nil {
		t.Fatal("clone should have nil debugFile")
	}
}

func TestClone_EmptySession(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	clone := s.Clone()

	if len(clone.messages) != 0 {
		t.Fatalf("expected empty messages, got %d", len(clone.messages))
	}
	if len(clone.systemPrompts) != 0 {
		t.Fatalf("expected empty system prompts, got %d", len(clone.systemPrompts))
	}
}

// ---------------------------------------------------------------------------
// 4. LoadMessages
// ---------------------------------------------------------------------------

func TestLoadMessages_ExtractsSystemPrompts(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "original-sys")

	msgs := []Message{
		{Role: RoleSystem, Content: "loaded-sys-1"},
		{Role: RoleSystem, Content: "loaded-sys-2"},
		{Role: RoleUser, Content: "user-msg"},
		{Role: RoleAssistant, Content: "asst-msg"},
	}
	s.LoadMessages(msgs)

	// System prompts should be replaced
	prompts := s.GetSystemPrompts()
	if len(prompts) != 2 {
		t.Fatalf("expected 2 system prompts, got %d", len(prompts))
	}
	if prompts[0] != "loaded-sys-1" || prompts[1] != "loaded-sys-2" {
		t.Fatalf("unexpected prompts: %v", prompts)
	}

	// Conversation messages should exclude system messages
	history := s.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected 2 conversation messages, got %d", len(history))
	}
	if history[0].Role != RoleUser || history[1].Role != RoleAssistant {
		t.Fatal("unexpected message roles in history")
	}
}

func TestLoadMessages_NoSystemPrompts_KeepsOriginal(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "original-sys")

	msgs := []Message{
		{Role: RoleUser, Content: "user-msg"},
		{Role: RoleAssistant, Content: "asst-msg"},
	}
	s.LoadMessages(msgs)

	// Original system prompt should be preserved when none are in loaded messages
	prompts := s.GetSystemPrompts()
	if len(prompts) != 1 || prompts[0] != "original-sys" {
		t.Fatalf("expected original system prompt preserved, got: %v", prompts)
	}
}

// ---------------------------------------------------------------------------
// 5. MessageStats
// ---------------------------------------------------------------------------

func TestMessageStats(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "system-prompt") // 13 bytes

	s.messages = []Message{
		{Role: RoleUser, Content: "hello"},     // 5 bytes
		{Role: RoleAssistant, Content: "world"}, // 5 bytes
		{Role: RoleUser, Content: "foo"},         // 3 bytes
		{Role: RoleAssistant, Content: "bar"},    // 3 bytes
		{Role: RoleSystem, Content: "injected"},  // 8 bytes (system in messages is unusual but counted)
	}

	stats := s.MessageStats()

	if stats.UserCount != 2 {
		t.Errorf("UserCount = %d, want 2", stats.UserCount)
	}
	if stats.AssistantCount != 2 {
		t.Errorf("AssistantCount = %d, want 2", stats.AssistantCount)
	}
	// SystemCount = 1 from messages + 1 from systemPrompts = 2
	if stats.SystemCount != 2 {
		t.Errorf("SystemCount = %d, want 2", stats.SystemCount)
	}
	// PayloadBytes = (5+5+3+3+8) from messages + 13 from systemPrompts = 37
	expectedBytes := len("hello") + len("world") + len("foo") + len("bar") + len("injected") + len("system-prompt")
	if stats.PayloadBytes != expectedBytes {
		t.Errorf("PayloadBytes = %d, want %d", stats.PayloadBytes, expectedBytes)
	}
}

func TestMessageStats_EmptySession(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	stats := s.MessageStats()

	if stats.UserCount != 0 || stats.AssistantCount != 0 || stats.SystemCount != 0 || stats.PayloadBytes != 0 {
		t.Errorf("expected all zeros for empty session, got %+v", stats)
	}
}

// ---------------------------------------------------------------------------
// 6. DropOldMessages
// ---------------------------------------------------------------------------

func TestDropOldMessages(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "sys")
	s.messages = []Message{
		{Role: RoleUser, Content: "msg-1"},
		{Role: RoleAssistant, Content: "msg-2"},
		{Role: RoleUser, Content: "msg-3"},
		{Role: RoleAssistant, Content: "msg-4"},
		{Role: RoleUser, Content: "msg-5"},
		{Role: RoleAssistant, Content: "msg-6"},
	}

	dropped := s.DropOldMessages(2)
	if dropped != 4 {
		t.Fatalf("expected 4 dropped, got %d", dropped)
	}
	if len(s.messages) != 2 {
		t.Fatalf("expected 2 remaining messages, got %d", len(s.messages))
	}
	if s.messages[0].Content != "msg-5" || s.messages[1].Content != "msg-6" {
		t.Fatalf("unexpected remaining messages: %v, %v", s.messages[0].Content, s.messages[1].Content)
	}

	// System prompts should be unaffected
	if len(s.GetSystemPrompts()) != 1 {
		t.Fatal("system prompts should not be affected by DropOldMessages")
	}
}

func TestDropOldMessages_KeepMoreThanExists(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.messages = []Message{
		{Role: RoleUser, Content: "only"},
	}

	dropped := s.DropOldMessages(10)
	if dropped != 0 {
		t.Fatalf("expected 0 dropped, got %d", dropped)
	}
	if len(s.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(s.messages))
	}
}

func TestDropOldMessages_KeepZero(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.messages = []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, Content: "b"},
	}

	dropped := s.DropOldMessages(0)
	if dropped != 2 {
		t.Fatalf("expected 2 dropped, got %d", dropped)
	}
	if len(s.messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(s.messages))
	}
}

// ---------------------------------------------------------------------------
// 7. Stop sequences
// ---------------------------------------------------------------------------

func TestSetStopSequences(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.SetStopSequences([]string{"END", "STOP"})

	got := s.GetStopSequences()
	if len(got) != 2 || got[0] != "END" || got[1] != "STOP" {
		t.Fatalf("unexpected stop sequences: %v", got)
	}
}

func TestStripStopSequences(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.SetStopSequences([]string{"<END>", "<STOP>"})

	tests := []struct {
		input string
		want  string
	}{
		{"hello<END>", "hello"},
		{"hello<STOP>world", "helloworld"},
		{"no stop here", "no stop here"},
		{"<END>prefix<STOP>suffix<END>", "prefixsuffix"},
		{"", ""},
	}
	for _, tt := range tests {
		got := s.stripStopSequences(tt.input)
		if got != tt.want {
			t.Errorf("stripStopSequences(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripStopSequences_NoSequencesSet(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	got := s.stripStopSequences("content<END>")
	if got != "content<END>" {
		t.Errorf("expected no stripping when no stop sequences set, got %q", got)
	}
}

func TestSend_StripsStopSequencesFromResponse(t *testing.T) {
	p := &mockProvider{
		chatResponse: &ChatResponse{ID: "r1", Content: "answer<STOP>"},
	}
	s := NewSession(p, "m")
	s.SetStopSequences([]string{"<STOP>"})

	resp, err := s.Send(context.Background(), "question")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "answer" {
		t.Errorf("expected stop sequence stripped from response, got %q", resp.Content)
	}

	// History should also have the stripped content
	history := s.GetHistory()
	if history[1].Content != "answer" {
		t.Errorf("history should have stripped content, got %q", history[1].Content)
	}
}

func TestSendStream_StripsStopSequences(t *testing.T) {
	p := &mockProvider{
		streamChunks: []StreamChunk{
			{Content: "ans<STOP>wer", Done: false},
			{Done: true, Usage: &Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}
	s := NewSession(p, "m")
	s.SetStopSequences([]string{"<STOP>"})

	resp, err := s.SendStream(context.Background(), "q", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "answer" {
		t.Errorf("expected 'answer', got %q", resp.Content)
	}
}

// ---------------------------------------------------------------------------
// 8. AddSystemPrompt
// ---------------------------------------------------------------------------

func TestAddSystemPrompt(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "initial")
	s.AddSystemPrompt("added-1")
	s.AddSystemPrompt("added-2")

	prompts := s.GetSystemPrompts()
	if len(prompts) != 3 {
		t.Fatalf("expected 3 system prompts, got %d", len(prompts))
	}
	if prompts[0] != "initial" || prompts[1] != "added-1" || prompts[2] != "added-2" {
		t.Fatalf("unexpected prompts: %v", prompts)
	}
}

func TestAddSystemPrompt_AppearsInBuildMessages(t *testing.T) {
	p := newMockProvider("ok")
	s := NewSession(p, "m", "sys-1")
	s.AddSystemPrompt("sys-2")

	_, err := s.Send(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	// Check that the request sent to the provider included both system prompts
	req := p.lastRequest
	if req == nil {
		t.Fatal("no request captured")
	}

	// First two messages should be system prompts
	if len(req.Messages) < 3 {
		t.Fatalf("expected at least 3 messages in request, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != RoleSystem || req.Messages[0].Content != "sys-1" {
		t.Errorf("first message should be sys-1, got %+v", req.Messages[0])
	}
	if req.Messages[1].Role != RoleSystem || req.Messages[1].Content != "sys-2" {
		t.Errorf("second message should be sys-2, got %+v", req.Messages[1])
	}
}

// ---------------------------------------------------------------------------
// 9. Multimodal messages (SendMessageStream)
// ---------------------------------------------------------------------------

func TestSendMessageStream_Multimodal(t *testing.T) {
	p := newMockProvider("image-response")
	s := NewSession(p, "m", "sys")

	userMsg := NewMultimodalMessage(RoleUser,
		ContentBlock{Type: ContentTypeText, Text: "describe this image"},
		ContentBlock{Type: ContentTypeImage, ImageData: &ImageBlock{
			Data:      "base64data",
			MediaType: "image/png",
		}},
	)

	var receivedChunks []string
	resp, err := s.SendMessageStream(context.Background(), userMsg, func(c StreamChunk) {
		receivedChunks = append(receivedChunks, c.Content)
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Content != "image-response" {
		t.Errorf("unexpected content: %q", resp.Content)
	}

	// The user message should be in history with Parts intact
	history := s.GetHistory()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}

	userHistoryMsg := history[0]
	if !userHistoryMsg.HasParts() {
		t.Fatal("user message in history should have Parts")
	}
	if len(userHistoryMsg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(userHistoryMsg.Parts))
	}
	if userHistoryMsg.Parts[0].Type != ContentTypeText {
		t.Error("first part should be text")
	}
	if userHistoryMsg.Parts[1].Type != ContentTypeImage {
		t.Error("second part should be image")
	}
	if userHistoryMsg.Parts[1].ImageData.Data != "base64data" {
		t.Error("image data mismatch")
	}

	// Assistant response should be plain text
	if history[1].Role != RoleAssistant || history[1].Content != "image-response" {
		t.Errorf("unexpected assistant message: %+v", history[1])
	}

	// Verify the provider received system prompt + user message
	req := p.lastRequest
	if req == nil {
		t.Fatal("no request captured")
	}
	// sys prompt + user multimodal message
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages in request, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != RoleSystem {
		t.Error("first request message should be system")
	}
	if !req.Messages[1].HasParts() {
		t.Error("user message in request should have Parts")
	}
}

func TestSendMessageStream_StripsStopSequences(t *testing.T) {
	p := &mockProvider{
		streamChunks: []StreamChunk{
			{Content: "response<END>tail", Done: false},
			{Done: true, Usage: &Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}
	s := NewSession(p, "m")
	s.SetStopSequences([]string{"<END>"})

	userMsg := NewTextMessage(RoleUser, "hello")
	resp, err := s.SendMessageStream(context.Background(), userMsg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "responsetail" {
		t.Errorf("expected stop sequence stripped, got %q", resp.Content)
	}
}

// ---------------------------------------------------------------------------
// 10. Compact
// ---------------------------------------------------------------------------

func TestCompact_NotEnoughMessages(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.messages = []Message{
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, Content: "a1"},
	}

	// turnRetention=1 means keep 2 messages, we only have 2
	compacted := s.Compact(1)
	if compacted != 0 {
		t.Fatalf("expected 0 compacted, got %d", compacted)
	}
	if len(s.messages) != 2 {
		t.Fatalf("messages should be unchanged, got %d", len(s.messages))
	}
}

func TestCompact_SummarizesOldTurns(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "sys")

	// 3 turns = 6 messages
	s.messages = []Message{
		{Role: RoleUser, Content: "first task"},
		{Role: RoleAssistant, Content: "did the first task"},
		{Role: RoleUser, Content: "<OBSERVATION>tool result</OBSERVATION>"},
		{Role: RoleAssistant, Content: "<ACTION>search</ACTION>\n<ACTION_INPUT>{}</ACTION_INPUT>"},
		{Role: RoleUser, Content: "latest question"},
		{Role: RoleAssistant, Content: "latest answer"},
	}

	// Keep last 1 turn (2 messages), compact the first 4
	compacted := s.Compact(1)
	if compacted != 4 {
		t.Fatalf("expected 4 compacted, got %d", compacted)
	}

	// Should have: 1 summary message + 2 retained messages = 3
	if len(s.messages) != 3 {
		t.Fatalf("expected 3 messages after compaction, got %d", len(s.messages))
	}

	// First message should be the summary
	summary := s.messages[0]
	if summary.Role != RoleUser {
		t.Errorf("summary should have role user, got %s", summary.Role)
	}
	if !strings.Contains(summary.Content, "<COMPACTED_CONTEXT>") {
		t.Error("summary should contain <COMPACTED_CONTEXT> tag")
	}
	if !strings.Contains(summary.Content, "</COMPACTED_CONTEXT>") {
		t.Error("summary should contain closing </COMPACTED_CONTEXT> tag")
	}
	if !strings.Contains(summary.Content, "first task") {
		t.Error("summary should contain the original task")
	}

	// Retained messages should be the last turn
	if s.messages[1].Content != "latest question" {
		t.Errorf("expected retained user msg, got %q", s.messages[1].Content)
	}
	if s.messages[2].Content != "latest answer" {
		t.Errorf("expected retained assistant msg, got %q", s.messages[2].Content)
	}
}

func TestCompact_PreservesLastNewTask(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	s.messages = []Message{
		{Role: RoleUser, Content: "original task"},
		{Role: RoleAssistant, Content: "ok"},
		{Role: RoleUser, Content: "<NEW_TASK>second task</NEW_TASK>"},
		{Role: RoleAssistant, Content: "ok second"},
		{Role: RoleUser, Content: "keep-this"},
		{Role: RoleAssistant, Content: "keep-this-too"},
	}

	s.Compact(1)

	summary := s.messages[0].Content
	// The summary should contain the NEW_TASK as the current task (it's the latest task message)
	if !strings.Contains(summary, "second task") {
		t.Error("summary should preserve the latest NEW_TASK as current task")
	}
}

func TestCompact_ExtractsToolCalls(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	s.messages = []Message{
		{Role: RoleUser, Content: "do something"},
		{Role: RoleAssistant, Content: "<ACTION>web_search</ACTION>"},
		{Role: RoleUser, Content: "<OBSERVATION>results</OBSERVATION>"},
		{Role: RoleAssistant, Content: "<ACTION>read_file</ACTION>"},
		{Role: RoleUser, Content: "<OBSERVATION>file content</OBSERVATION>"},
		{Role: RoleAssistant, Content: "done"},
		{Role: RoleUser, Content: "final-q"},
		{Role: RoleAssistant, Content: "final-a"},
	}

	s.Compact(1)

	summary := s.messages[0].Content
	if !strings.Contains(summary, "Tools used:") {
		t.Error("summary should list tools used")
	}
	if !strings.Contains(summary, "web_search") {
		t.Error("summary should mention web_search")
	}
	if !strings.Contains(summary, "read_file") {
		t.Error("summary should mention read_file")
	}
}

func TestCompact_ExtractsAnswers(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	s.messages = []Message{
		{Role: RoleUser, Content: "question"},
		{Role: RoleAssistant, Content: "reasoning <ANSWER>the answer is 42</ANSWER>"},
		{Role: RoleUser, Content: "final-q"},
		{Role: RoleAssistant, Content: "final-a"},
	}

	s.Compact(1)

	summary := s.messages[0].Content
	if !strings.Contains(summary, "the answer is 42") {
		t.Error("summary should contain extracted answer")
	}
	if !strings.Contains(summary, "Key findings:") {
		t.Error("summary should have Key findings section")
	}
}

func TestCompact_EmptyConversationFallback(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	// Messages with no tool calls, no answers, no tasks — just plain conversation
	s.messages = []Message{
		{Role: RoleAssistant, Content: "just thinking"},
		{Role: RoleAssistant, Content: "more thinking"},
		{Role: RoleUser, Content: "final-q"},
		{Role: RoleAssistant, Content: "final-a"},
	}

	s.Compact(1)

	summary := s.messages[0].Content
	if !strings.Contains(summary, "general reasoning and exploration") {
		t.Error("summary should contain fallback text when no tools/answers found")
	}
}

// ---------------------------------------------------------------------------
// Helpers: buildMessages, buildCurrentMessages, MessageCount
// ---------------------------------------------------------------------------

func TestBuildMessages_IncludesSystemPromptsAndHistory(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "sys-a", "sys-b")
	s.messages = []Message{
		{Role: RoleUser, Content: "prev-q"},
		{Role: RoleAssistant, Content: "prev-a"},
	}

	msgs := s.buildMessages("new-q")

	// sys-a, sys-b, prev-q, prev-a, new-q
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	if msgs[0].Role != RoleSystem || msgs[0].Content != "sys-a" {
		t.Error("first should be sys-a")
	}
	if msgs[1].Role != RoleSystem || msgs[1].Content != "sys-b" {
		t.Error("second should be sys-b")
	}
	if msgs[4].Role != RoleUser || msgs[4].Content != "new-q" {
		t.Error("last should be new-q")
	}
}

func TestBuildCurrentMessages_NoNewUserMessage(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "sys")
	s.messages = []Message{
		{Role: RoleUser, Content: "q"},
	}

	msgs := s.buildCurrentMessages()

	// sys + q = 2
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != RoleSystem {
		t.Error("first should be system")
	}
	if msgs[1].Role != RoleUser && msgs[1].Content != "q" {
		t.Error("second should be the existing user message")
	}
}

func TestMessageCount(t *testing.T) {
	s := NewSession(&mockProvider{}, "m", "sys")
	if s.MessageCount() != 0 {
		t.Fatalf("expected 0, got %d", s.MessageCount())
	}

	s.messages = append(s.messages, Message{Role: RoleUser, Content: "a"})
	s.messages = append(s.messages, Message{Role: RoleAssistant, Content: "b"})

	if s.MessageCount() != 2 {
		t.Fatalf("expected 2, got %d", s.MessageCount())
	}
}

// ---------------------------------------------------------------------------
// ContinueStream
// ---------------------------------------------------------------------------

func TestContinueStream_AppendsOnlyAssistantMessage(t *testing.T) {
	p := newMockProvider("continued-reply")
	s := NewSession(p, "m", "sys")

	// Simulate an existing pending user message already in history
	s.messages = []Message{
		{Role: RoleUser, Content: "pending-question"},
	}

	resp, err := s.ContinueStream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "continued-reply" {
		t.Errorf("expected 'continued-reply', got %q", resp.Content)
	}

	// Should only have appended the assistant message, not another user message
	if len(s.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.messages))
	}
	if s.messages[0].Role != RoleUser || s.messages[0].Content != "pending-question" {
		t.Error("first message should be unchanged user message")
	}
	if s.messages[1].Role != RoleAssistant || s.messages[1].Content != "continued-reply" {
		t.Error("second message should be the continued assistant reply")
	}
}

// ---------------------------------------------------------------------------
// CompactWithContext
// ---------------------------------------------------------------------------

func TestCompactWithContext_InjectsExtraContext(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	s.messages = []Message{
		{Role: RoleUser, Content: "task-1"},
		{Role: RoleAssistant, Content: "done-1"},
		{Role: RoleUser, Content: "task-2"},
		{Role: RoleAssistant, Content: "done-2"},
		{Role: RoleUser, Content: "keep"},
		{Role: RoleAssistant, Content: "keep-too"},
	}

	compacted := s.CompactWithContext(1, "EXTRA: dataset progress 50%")
	if compacted != 4 {
		t.Fatalf("expected 4 compacted, got %d", compacted)
	}

	summary := s.messages[0].Content
	if !strings.Contains(summary, "EXTRA: dataset progress 50%") {
		t.Error("summary should contain the injected extra context")
	}
	if !strings.Contains(summary, "<COMPACTED_CONTEXT>") {
		t.Error("summary should still have compaction tags")
	}
}

func TestCompactWithContext_EmptyExtraContext(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	s.messages = []Message{
		{Role: RoleUser, Content: "old"},
		{Role: RoleAssistant, Content: "old-reply"},
		{Role: RoleUser, Content: "new"},
		{Role: RoleAssistant, Content: "new-reply"},
	}

	compacted := s.CompactWithContext(1, "")
	if compacted != 2 {
		t.Fatalf("expected 2 compacted, got %d", compacted)
	}

	// Should work the same as regular Compact when extra context is empty
	if !strings.Contains(s.messages[0].Content, "<COMPACTED_CONTEXT>") {
		t.Error("summary should still be generated")
	}
}

// ---------------------------------------------------------------------------
// SetPromptCaching
// ---------------------------------------------------------------------------

func TestSetPromptCaching(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")

	s.SetPromptCaching(true, true)
	if !s.promptCaching {
		t.Error("promptCaching should be true")
	}
	if !s.conversationCaching {
		t.Error("conversationCaching should be true")
	}

	s.SetPromptCaching(true, false)
	if !s.promptCaching {
		t.Error("promptCaching should still be true")
	}
	if s.conversationCaching {
		t.Error("conversationCaching should be false")
	}

	s.SetPromptCaching(false, false)
	if s.promptCaching {
		t.Error("promptCaching should be false")
	}
}

func TestSend_PassesCachingFlagsToRequest(t *testing.T) {
	p := newMockProvider("ok")
	s := NewSession(p, "m")
	s.SetPromptCaching(true, true)

	_, err := s.Send(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	if !p.lastRequest.PromptCaching {
		t.Error("request should have PromptCaching=true")
	}
	if !p.lastRequest.ConversationCaching {
		t.Error("request should have ConversationCaching=true")
	}
}

// ---------------------------------------------------------------------------
// SnapshotMessages
// ---------------------------------------------------------------------------

func TestSnapshotMessages_SharesUnderlying(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.messages = []Message{
		{Role: RoleUser, Content: "a"},
	}

	snap := s.SnapshotMessages()
	if len(snap) != 1 || snap[0].Content != "a" {
		t.Fatal("snapshot should reflect current messages")
	}
}

// ---------------------------------------------------------------------------
// Usage capture
// ---------------------------------------------------------------------------

func TestSendStream_CapturesUsage(t *testing.T) {
	p := &mockProvider{
		streamChunks: []StreamChunk{
			{Content: "text", Done: false},
			{Done: true, Usage: &Usage{InputTokens: 100, OutputTokens: 50, CacheWriteTokens: 10, CacheReadTokens: 5}},
		},
	}
	s := NewSession(p, "m")

	resp, err := s.SendStream(context.Background(), "q", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
	if resp.Usage.CacheWriteTokens != 10 || resp.Usage.CacheReadTokens != 5 {
		t.Errorf("unexpected cache usage: %+v", resp.Usage)
	}
}

// ---------------------------------------------------------------------------
// uniqueStrings helper
// ---------------------------------------------------------------------------

func TestUniqueStrings(t *testing.T) {
	tests := []struct {
		input []string
		want  []string
	}{
		{[]string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{[]string{"x"}, []string{"x"}},
		{[]string{}, []string{}},
		{nil, []string{}},
	}

	for _, tt := range tests {
		got := uniqueStrings(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("uniqueStrings(%v) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("uniqueStrings(%v)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
