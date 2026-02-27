package wsbridge

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mlund01/squadron-sdk/protocol"

	"squadron/streamers"
)

// WSMissionHandler implements streamers.MissionHandler by sending events over WebSocket to commander.
type WSMissionHandler struct {
	client *Client

	mu          sync.Mutex
	missionID   string
	missionIDCh chan string // signals when mission ID is available
}

// NewWSMissionHandler creates a new WebSocket-backed mission handler.
func NewWSMissionHandler(client *Client) *WSMissionHandler {
	return &WSMissionHandler{
		client:      client,
		missionIDCh: make(chan string, 1),
	}
}

// MissionID returns the mission ID (available after MissionStarted is called).
func (h *WSMissionHandler) MissionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.missionID
}

// WaitForMissionID blocks until MissionStarted fires and the mission ID is known, or times out.
func (h *WSMissionHandler) WaitForMissionID(timeout time.Duration) (string, error) {
	select {
	case id := <-h.missionIDCh:
		return id, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("timeout waiting for mission to start")
	}
}

func (h *WSMissionHandler) sendEvent(eventType protocol.MissionEventType, data interface{}) {
	h.mu.Lock()
	mid := h.missionID
	h.mu.Unlock()

	env, err := protocol.NewEvent(protocol.TypeMissionEvent, &protocol.MissionEventPayload{
		MissionID: mid,
		EventType: eventType,
		Data:      data,
	})
	if err != nil {
		log.Printf("WSMissionHandler: marshal event: %v", err)
		return
	}
	if err := h.client.SendEvent(env); err != nil {
		log.Printf("WSMissionHandler: send event: %v", err)
	}
}

// =============================================================================
// MissionHandler implementation
// =============================================================================

func (h *WSMissionHandler) MissionStarted(name string, missionID string, taskCount int) {
	h.mu.Lock()
	h.missionID = missionID
	h.mu.Unlock()

	// Notify anyone waiting for the mission ID
	select {
	case h.missionIDCh <- missionID:
	default:
	}

	h.sendEvent(protocol.EventMissionStarted, protocol.MissionStartedData{
		MissionName: name,
		MissionID:   missionID,
		TaskCount:   taskCount,
	})
}

func (h *WSMissionHandler) MissionCompleted(name string) {
	h.sendEvent(protocol.EventMissionCompleted, protocol.MissionCompletedData{
		MissionName: name,
	})
}

func (h *WSMissionHandler) TaskStarted(taskName string, objective string) {
	h.sendEvent(protocol.EventTaskStarted, protocol.TaskStartedData{
		TaskName:  taskName,
		Objective: objective,
	})
}

func (h *WSMissionHandler) TaskCompleted(taskName string, summary string) {
	h.sendEvent(protocol.EventTaskCompleted, protocol.TaskCompletedData{
		TaskName: taskName,
		Summary:  summary,
	})
}

func (h *WSMissionHandler) TaskFailed(taskName string, err error) {
	h.sendEvent(protocol.EventTaskFailed, protocol.TaskFailedData{
		TaskName: taskName,
		Error:    err.Error(),
	})
}

func (h *WSMissionHandler) TaskIterationStarted(taskName string, totalItems int, parallel bool) {
	h.sendEvent(protocol.EventTaskIterationStarted, protocol.TaskIterationStartedData{
		TaskName:   taskName,
		TotalItems: totalItems,
		Parallel:   parallel,
	})
}

func (h *WSMissionHandler) TaskIterationCompleted(taskName string, completedCount int, workingSummary string) {
	h.sendEvent(protocol.EventTaskIterationCompleted, protocol.TaskIterationCompletedData{
		TaskName:       taskName,
		CompletedCount: completedCount,
		WorkingSummary: workingSummary,
	})
}

func (h *WSMissionHandler) IterationStarted(taskName string, index int, objective string) {
	h.sendEvent(protocol.EventIterationStarted, protocol.IterationStartedData{
		TaskName:  taskName,
		Index:     index,
		Objective: objective,
	})
}

func (h *WSMissionHandler) IterationCompleted(taskName string, index int, summary string) {
	h.sendEvent(protocol.EventIterationCompleted, protocol.IterationCompletedData{
		TaskName: taskName,
		Index:    index,
		Summary:  summary,
	})
}

func (h *WSMissionHandler) IterationFailed(taskName string, index int, err error) {
	h.sendEvent(protocol.EventIterationFailed, protocol.IterationFailedData{
		TaskName: taskName,
		Index:    index,
		Error:    err.Error(),
	})
}

func (h *WSMissionHandler) IterationRetrying(taskName string, index int, attempt int, maxRetries int, err error) {
	h.sendEvent(protocol.EventIterationRetrying, protocol.IterationRetryingData{
		TaskName:   taskName,
		Index:      index,
		Attempt:    attempt,
		MaxRetries: maxRetries,
		Error:      err.Error(),
	})
}

func (h *WSMissionHandler) IterationReasoning(taskName string, index int, content string) {
	h.sendEvent(protocol.EventIterationReasoning, protocol.IterationReasoningData{
		TaskName: taskName,
		Index:    index,
		Content:  content,
	})
}

func (h *WSMissionHandler) IterationAnswer(taskName string, index int, content string) {
	h.sendEvent(protocol.EventIterationAnswer, protocol.IterationAnswerData{
		TaskName: taskName,
		Index:    index,
		Content:  content,
	})
}

func (h *WSMissionHandler) SummaryAggregation(taskName string, summaryCount int) {
	h.sendEvent(protocol.EventSummaryAggregation, protocol.SummaryAggregationData{
		TaskName:     taskName,
		SummaryCount: summaryCount,
	})
}

func (h *WSMissionHandler) CommanderReasoning(taskName string, content string) {
	h.sendEvent(protocol.EventCommanderReasoning, protocol.CommanderReasoningData{
		TaskName: taskName,
		Content:  content,
	})
}

func (h *WSMissionHandler) CommanderAnswer(taskName string, content string) {
	h.sendEvent(protocol.EventCommanderAnswer, protocol.CommanderAnswerData{
		TaskName: taskName,
		Content:  content,
	})
}

func (h *WSMissionHandler) CommanderCallingTool(taskName string, toolName string, input string) {
	h.sendEvent(protocol.EventCommanderCallingTool, protocol.CommanderCallingToolData{
		TaskName: taskName,
		ToolName: toolName,
		Input:    input,
	})
}

func (h *WSMissionHandler) CommanderToolComplete(taskName string, toolName string) {
	h.sendEvent(protocol.EventCommanderToolComplete, protocol.CommanderToolCompleteData{
		TaskName: taskName,
		ToolName: toolName,
	})
}

func (h *WSMissionHandler) AgentStarted(taskName string, agentName string) {
	h.sendEvent(protocol.EventAgentStarted, protocol.AgentStartedData{
		TaskName:  taskName,
		AgentName: agentName,
	})
}

func (h *WSMissionHandler) AgentHandler(taskName string, agentName string) streamers.ChatHandler {
	return &wsChatHandler{
		parent:    h,
		taskName:  taskName,
		agentName: agentName,
	}
}

func (h *WSMissionHandler) AgentCompleted(taskName string, agentName string) {
	h.sendEvent(protocol.EventAgentCompleted, protocol.AgentCompletedData{
		TaskName:  taskName,
		AgentName: agentName,
	})
}

// =============================================================================
// wsChatHandler — WebSocket ChatHandler for agent events
// =============================================================================

type wsChatHandler struct {
	parent    *WSMissionHandler
	taskName  string
	agentName string
}

func (c *wsChatHandler) Welcome(agentName string, modelName string) {}

func (c *wsChatHandler) AwaitClientAnswer() (string, error) {
	return "", nil
}

func (c *wsChatHandler) Goodbye() {}

func (c *wsChatHandler) Error(err error) {}

func (c *wsChatHandler) Thinking() {
	c.parent.sendEvent(protocol.EventAgentThinking, protocol.AgentThinkingData{
		TaskName:  c.taskName,
		AgentName: c.agentName,
	})
}

func (c *wsChatHandler) CallingTool(toolName string, payload string) {
	c.parent.sendEvent(protocol.EventAgentCallingTool, protocol.AgentCallingToolData{
		TaskName:  c.taskName,
		AgentName: c.agentName,
		ToolName:  toolName,
		Payload:   payload,
	})
}

func (c *wsChatHandler) ToolComplete(toolName string) {
	c.parent.sendEvent(protocol.EventAgentToolComplete, protocol.AgentToolCompleteData{
		TaskName:  c.taskName,
		AgentName: c.agentName,
		ToolName:  toolName,
	})
}

func (c *wsChatHandler) PublishReasoningChunk(chunk string) {
	// High-volume streaming chunks are not sent over WS individually
}

func (c *wsChatHandler) FinishReasoning() {}

func (c *wsChatHandler) PublishAnswerChunk(chunk string) {
	// High-volume streaming chunks are not sent over WS individually
}

func (c *wsChatHandler) FinishAnswer() {
	c.parent.sendEvent(protocol.EventAgentAnswer, protocol.AgentAnswerData{
		TaskName:  c.taskName,
		AgentName: c.agentName,
	})
}
