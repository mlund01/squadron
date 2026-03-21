package streamers

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"squadron/store"

	"github.com/mlund01/squadron-sdk/protocol"
)

// StoringMissionHandler is a MissionHandler decorator that persists every event
// to the EventStore, then delegates to an inner handler (e.g. CLI or WebSocket).
type StoringMissionHandler struct {
	inner  MissionHandler
	events store.EventStore

	mu         sync.Mutex
	missionID  string
	taskIDs    map[string]string // taskName → taskID
	sessionIDs map[string]string // "taskName:agentName" → sessionID
}

// NewStoringMissionHandler wraps an existing MissionHandler with event persistence.
func NewStoringMissionHandler(inner MissionHandler, events store.EventStore) *StoringMissionHandler {
	return &StoringMissionHandler{
		inner:      inner,
		events:     events,
		taskIDs:    make(map[string]string),
		sessionIDs: make(map[string]string),
	}
}

// store persists an event, logging (not failing) on error.
func (h *StoringMissionHandler) storeEvent(eventType protocol.MissionEventType, taskName *string, sessionKey *string, iterationIndex *int, data interface{}) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		log.Printf("StoringMissionHandler: marshal event data: %v", err)
		return
	}

	h.mu.Lock()
	missionID := h.missionID
	var taskID *string
	if taskName != nil {
		if tid, ok := h.taskIDs[*taskName]; ok {
			taskID = &tid
		} else if bracketIdx := strings.LastIndex(*taskName, "["); bracketIdx != -1 {
			// Iterated tasks use "taskName[N]" but IDs are registered under "taskName"
			if tid, ok := h.taskIDs[(*taskName)[:bracketIdx]]; ok {
				taskID = &tid
			}
		}
	}
	var sessionID *string
	if sessionKey != nil {
		if sid, ok := h.sessionIDs[*sessionKey]; ok {
			sessionID = &sid
		} else if bracketIdx := strings.LastIndex(*sessionKey, "["); bracketIdx != -1 {
			// Strip iteration suffix from session key (e.g. "write_story[0]:commander" → "write_story:commander")
			colonIdx := strings.LastIndex(*sessionKey, ":")
			if colonIdx > bracketIdx {
				baseKey := (*sessionKey)[:bracketIdx] + (*sessionKey)[colonIdx:]
				if sid, ok := h.sessionIDs[baseKey]; ok {
					sessionID = &sid
				}
			}
		}
	}
	h.mu.Unlock()

	event := store.MissionEvent{
		ID:             generateEventID(),
		MissionID:      missionID,
		TaskID:         taskID,
		SessionID:      sessionID,
		IterationIndex: iterationIndex,
		EventType:      string(eventType),
		DataJSON:       string(dataJSON),
		CreatedAt:      time.Now(),
	}

	if err := h.events.StoreEvent(event); err != nil {
		log.Printf("StoringMissionHandler: store event: %v", err)
	}
}

func (h *StoringMissionHandler) setTaskID(taskName, taskID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.taskIDs[taskName] = taskID
}

func (h *StoringMissionHandler) setSessionID(key, sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionIDs[key] = sessionID
}

// SetTaskID allows the runner to register task IDs as tasks are created.
func (h *StoringMissionHandler) SetTaskID(taskName, taskID string) {
	h.setTaskID(taskName, taskID)
}

// SetSessionID allows the runner to register session IDs as sessions are created.
func (h *StoringMissionHandler) SetSessionID(taskName, agentName, sessionID string) {
	h.setSessionID(taskName+":"+agentName, sessionID)
}

// =============================================================================
// MissionHandler implementation
// =============================================================================

func (h *StoringMissionHandler) MissionStarted(name string, missionID string, taskCount int) {
	h.mu.Lock()
	h.missionID = missionID
	h.mu.Unlock()

	h.storeEvent(protocol.EventMissionStarted, nil, nil, nil, protocol.MissionStartedData{
		MissionName: name,
		MissionID:   missionID,
		TaskCount:   taskCount,
	})
	h.inner.MissionStarted(name, missionID, taskCount)
}

func (h *StoringMissionHandler) MissionCompleted(name string) {
	h.storeEvent(protocol.EventMissionCompleted, nil, nil, nil, protocol.MissionCompletedData{
		MissionName: name,
	})
	h.inner.MissionCompleted(name)
}

func (h *StoringMissionHandler) TaskStarted(taskName string, objective string) {
	h.storeEvent(protocol.EventTaskStarted, &taskName, nil, nil, protocol.TaskStartedData{
		TaskName:  taskName,
		Objective: objective,
	})
	h.inner.TaskStarted(taskName, objective)
}

func (h *StoringMissionHandler) TaskCompleted(taskName string) {
	h.storeEvent(protocol.EventTaskCompleted, &taskName, nil, nil, protocol.TaskCompletedData{
		TaskName: taskName,
	})
	h.inner.TaskCompleted(taskName)
}

func (h *StoringMissionHandler) TaskFailed(taskName string, err error) {
	h.storeEvent(protocol.EventTaskFailed, &taskName, nil, nil, protocol.TaskFailedData{
		TaskName: taskName,
		Error:    err.Error(),
	})
	h.inner.TaskFailed(taskName, err)
}

func (h *StoringMissionHandler) TaskIterationStarted(taskName string, totalItems int, parallel bool) {
	h.storeEvent(protocol.EventTaskIterationStarted, &taskName, nil, nil, protocol.TaskIterationStartedData{
		TaskName:   taskName,
		TotalItems: totalItems,
		Parallel:   parallel,
	})
	h.inner.TaskIterationStarted(taskName, totalItems, parallel)
}

func (h *StoringMissionHandler) TaskIterationCompleted(taskName string, completedCount int) {
	h.storeEvent(protocol.EventTaskIterationCompleted, &taskName, nil, nil, protocol.TaskIterationCompletedData{
		TaskName:       taskName,
		CompletedCount: completedCount,
	})
	h.inner.TaskIterationCompleted(taskName, completedCount)
}

func (h *StoringMissionHandler) IterationStarted(taskName string, index int, objective string) {
	h.storeEvent(protocol.EventIterationStarted, &taskName, nil, &index, protocol.IterationStartedData{
		TaskName:  taskName,
		Index:     index,
		Objective: objective,
	})
	h.inner.IterationStarted(taskName, index, objective)
}

func (h *StoringMissionHandler) IterationCompleted(taskName string, index int) {
	h.storeEvent(protocol.EventIterationCompleted, &taskName, nil, &index, protocol.IterationCompletedData{
		TaskName: taskName,
		Index:    index,
	})
	h.inner.IterationCompleted(taskName, index)
}

func (h *StoringMissionHandler) IterationFailed(taskName string, index int, err error) {
	h.storeEvent(protocol.EventIterationFailed, &taskName, nil, &index, protocol.IterationFailedData{
		TaskName: taskName,
		Index:    index,
		Error:    err.Error(),
	})
	h.inner.IterationFailed(taskName, index, err)
}

func (h *StoringMissionHandler) IterationRetrying(taskName string, index int, attempt int, maxRetries int, err error) {
	h.storeEvent(protocol.EventIterationRetrying, &taskName, nil, &index, protocol.IterationRetryingData{
		TaskName:   taskName,
		Index:      index,
		Attempt:    attempt,
		MaxRetries: maxRetries,
		Error:      err.Error(),
	})
	h.inner.IterationRetrying(taskName, index, attempt, maxRetries, err)
}

func (h *StoringMissionHandler) IterationReasoning(taskName string, index int, content string) {
	sessionKey := taskName + ":commander"
	h.storeEvent(protocol.EventIterationReasoning, &taskName, &sessionKey, &index, protocol.IterationReasoningData{
		TaskName: taskName,
		Index:    index,
		Content:  content,
	})
	h.inner.IterationReasoning(taskName, index, content)
}

func (h *StoringMissionHandler) IterationAnswer(taskName string, index int, content string) {
	sessionKey := taskName + ":commander"
	h.storeEvent(protocol.EventIterationAnswer, &taskName, &sessionKey, &index, protocol.IterationAnswerData{
		TaskName: taskName,
		Index:    index,
		Content:  content,
	})
	h.inner.IterationAnswer(taskName, index, content)
}

func (h *StoringMissionHandler) CommanderReasoning(taskName string, content string) {
	sessionKey := taskName + ":commander"
	h.storeEvent(protocol.EventCommanderReasoning, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.CommanderReasoningData{
		TaskName: taskName,
		Content:  content,
	})
	h.inner.CommanderReasoning(taskName, content)
}

func (h *StoringMissionHandler) CommanderAnswer(taskName string, content string) {
	sessionKey := taskName + ":commander"
	h.storeEvent(protocol.EventCommanderAnswer, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.CommanderAnswerData{
		TaskName: taskName,
		Content:  content,
	})
	h.inner.CommanderAnswer(taskName, content)
}

func (h *StoringMissionHandler) CommanderCallingTool(taskName string, toolName string, input string) {
	sessionKey := taskName + ":commander"
	h.storeEvent(protocol.EventCommanderCallingTool, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.CommanderCallingToolData{
		TaskName: taskName,
		ToolName: toolName,
		Input:    input,
	})
	h.inner.CommanderCallingTool(taskName, toolName, input)
}

func (h *StoringMissionHandler) CommanderToolComplete(taskName string, toolName string, result string) {
	sessionKey := taskName + ":commander"
	h.storeEvent(protocol.EventCommanderToolComplete, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.CommanderToolCompleteData{
		TaskName: taskName,
		ToolName: toolName,
		Result:   result,
	})
	h.inner.CommanderToolComplete(taskName, toolName, result)
}

func (h *StoringMissionHandler) Compaction(taskName string, entity string, inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int) {
	sessionKey := taskName + ":" + entity
	h.storeEvent(protocol.EventCompaction, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.CompactionData{
		TaskName:          taskName,
		Entity:            entity,
		InputTokens:       inputTokens,
		TokenLimit:        tokenLimit,
		MessagesCompacted: messagesCompacted,
		TurnRetention:     turnRetention,
	})
	h.inner.Compaction(taskName, entity, inputTokens, tokenLimit, messagesCompacted, turnRetention)
}

func (h *StoringMissionHandler) SessionTurn(data protocol.SessionTurnData) {
	sessionKey := data.TaskName + ":" + data.Entity
	h.storeEvent(protocol.EventSessionTurn, &data.TaskName, &sessionKey, extractIterationIndex(data.TaskName), data)
	h.inner.SessionTurn(data)
}

func (h *StoringMissionHandler) AgentStarted(taskName string, agentName string) {
	sessionKey := taskName + ":" + agentName
	h.storeEvent(protocol.EventAgentStarted, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.AgentStartedData{
		TaskName:  taskName,
		AgentName: agentName,
	})
	h.inner.AgentStarted(taskName, agentName)
}

func (h *StoringMissionHandler) AgentHandler(taskName string, agentName string) ChatHandler {
	innerCH := h.inner.AgentHandler(taskName, agentName)
	sessionKey := taskName + ":" + agentName
	return &storingChatHandler{
		inner:      innerCH,
		parent:     h,
		taskName:   taskName,
		agentName:  agentName,
		sessionKey: sessionKey,
	}
}

func (h *StoringMissionHandler) AgentCompleted(taskName string, agentName string) {
	sessionKey := taskName + ":" + agentName
	h.storeEvent(protocol.EventAgentCompleted, &taskName, &sessionKey, extractIterationIndex(taskName), protocol.AgentCompletedData{
		TaskName:  taskName,
		AgentName: agentName,
	})
	h.inner.AgentCompleted(taskName, agentName)
}

// =============================================================================
// storingChatHandler — wraps ChatHandler for agent-level events
// =============================================================================

type storingChatHandler struct {
	inner      ChatHandler
	parent     *StoringMissionHandler
	taskName   string
	agentName  string
	sessionKey string
	reasoningBuf strings.Builder
	answerBuf    strings.Builder
}

func (c *storingChatHandler) Welcome(agentName string, modelName string) {
	c.inner.Welcome(agentName, modelName)
}

func (c *storingChatHandler) AwaitClientAnswer() (string, error) {
	return c.inner.AwaitClientAnswer()
}

func (c *storingChatHandler) Goodbye() {
	c.inner.Goodbye()
}

func (c *storingChatHandler) Error(err error) {
	c.inner.Error(err)
}

func (c *storingChatHandler) Thinking() {
	c.reasoningBuf.Reset()
	c.inner.Thinking()
}

func (c *storingChatHandler) CallingTool(toolName string, payload string) {
	c.parent.storeEvent(protocol.EventAgentCallingTool, &c.taskName, &c.sessionKey, extractIterationIndex(c.taskName), protocol.AgentCallingToolData{
		TaskName:  c.taskName,
		AgentName: c.agentName,
		ToolName:  toolName,
		Payload:   payload,
	})
	c.inner.CallingTool(toolName, payload)
}

func (c *storingChatHandler) ToolComplete(toolName string, result string) {
	c.parent.storeEvent(protocol.EventAgentToolComplete, &c.taskName, &c.sessionKey, extractIterationIndex(c.taskName), protocol.AgentToolCompleteData{
		TaskName:  c.taskName,
		AgentName: c.agentName,
		ToolName:  toolName,
		Result:    result,
	})
	c.inner.ToolComplete(toolName, result)
}

func (c *storingChatHandler) PublishReasoningChunk(chunk string) {
	c.reasoningBuf.WriteString(chunk)
	c.inner.PublishReasoningChunk(chunk)
}

func (c *storingChatHandler) FinishReasoning() {
	if c.reasoningBuf.Len() > 0 {
		c.parent.storeEvent(protocol.EventAgentThinking, &c.taskName, &c.sessionKey, extractIterationIndex(c.taskName), protocol.AgentThinkingData{
			TaskName:  c.taskName,
			AgentName: c.agentName,
			Content:   c.reasoningBuf.String(),
		})
		c.reasoningBuf.Reset()
	}
	c.inner.FinishReasoning()
}

func (c *storingChatHandler) PublishAnswerChunk(chunk string) {
	c.answerBuf.WriteString(chunk)
	c.inner.PublishAnswerChunk(chunk)
}

func (c *storingChatHandler) FinishAnswer() {
	if c.answerBuf.Len() > 0 {
		c.parent.storeEvent(protocol.EventAgentAnswer, &c.taskName, &c.sessionKey, extractIterationIndex(c.taskName), protocol.AgentAnswerData{
			TaskName:  c.taskName,
			AgentName: c.agentName,
			Content:   c.answerBuf.String(),
		})
		c.answerBuf.Reset()
	}
	c.inner.FinishAnswer()
}

// =============================================================================
// Helpers
// =============================================================================

func generateEventID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().UnixNano()%100000)
}

// extractIterationIndex parses an iteration index from a task name like "greet[2]".
func extractIterationIndex(taskName string) *int {
	idx := strings.LastIndex(taskName, "[")
	if idx == -1 {
		return nil
	}
	end := strings.LastIndex(taskName, "]")
	if end <= idx {
		return nil
	}
	n, err := strconv.Atoi(taskName[idx+1 : end])
	if err != nil {
		return nil
	}
	return &n
}
