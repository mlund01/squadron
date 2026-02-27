package wsbridge

import (
	"log"
	"strings"
	"sync"

	"github.com/mlund01/squadron-sdk/protocol"
)

// WSChatHandler implements streamers.ChatHandler by streaming every chunk
// over the WebSocket connection to commander in real-time.
type WSChatHandler struct {
	client    *Client
	sessionID string
	mu        sync.Mutex
	answer    strings.Builder // accumulates answer chunks for persistence
}

// NewWSChatHandler creates a new WebSocket-backed chat handler for interactive agent chat.
func NewWSChatHandler(client *Client, sessionID string) *WSChatHandler {
	return &WSChatHandler{
		client:    client,
		sessionID: sessionID,
	}
}

func (h *WSChatHandler) sendChatEvent(eventType protocol.ChatEventType, data interface{}) {
	env, err := protocol.NewEvent(protocol.TypeChatEvent, &protocol.ChatEventPayload{
		SessionID: h.sessionID,
		EventType: eventType,
		Data:      data,
	})
	if err != nil {
		log.Printf("WSChatHandler: marshal event: %v", err)
		return
	}
	if err := h.client.SendEvent(env); err != nil {
		log.Printf("WSChatHandler: send event: %v", err)
	}
}

func (h *WSChatHandler) Welcome(agentName string, modelName string) {}

func (h *WSChatHandler) AwaitClientAnswer() (string, error) {
	return "", nil
}

func (h *WSChatHandler) Goodbye() {}

func (h *WSChatHandler) Error(err error) {
	h.sendChatEvent(protocol.ChatEventError, protocol.ChatErrorData{
		Message: err.Error(),
	})
}

func (h *WSChatHandler) Thinking() {
	h.sendChatEvent(protocol.ChatEventThinking, nil)
}

func (h *WSChatHandler) CallingTool(toolName string, payload string) {
	h.sendChatEvent(protocol.ChatEventCallingTool, protocol.ChatToolData{
		ToolName: toolName,
		Payload:  payload,
	})
}

func (h *WSChatHandler) ToolComplete(toolName string) {
	h.sendChatEvent(protocol.ChatEventToolComplete, protocol.ChatToolData{
		ToolName: toolName,
	})
}

func (h *WSChatHandler) PublishReasoningChunk(chunk string) {
	h.sendChatEvent(protocol.ChatEventReasoningChunk, protocol.ChatChunkData{
		Content: chunk,
	})
}

func (h *WSChatHandler) FinishReasoning() {
	h.sendChatEvent(protocol.ChatEventReasoningDone, nil)
}

func (h *WSChatHandler) PublishAnswerChunk(chunk string) {
	h.mu.Lock()
	h.answer.WriteString(chunk)
	h.mu.Unlock()
	h.sendChatEvent(protocol.ChatEventAnswerChunk, protocol.ChatChunkData{
		Content: chunk,
	})
}

func (h *WSChatHandler) FinishAnswer() {
	h.sendChatEvent(protocol.ChatEventAnswerDone, nil)
}

// FullAnswer returns the accumulated answer text for persistence.
func (h *WSChatHandler) FullAnswer() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.answer.String()
}
