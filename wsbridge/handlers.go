package wsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mlund01/squadron-wire/protocol"

	"squadron/agent"
	"squadron/config"
	"squadron/mission"
	"squadron/store"
	"squadron/streamers"
)

func (c *Client) registerHandlers() {
	c.handlers[protocol.TypeGetConfig] = c.handleGetConfig
	c.handlers[protocol.TypeRunMission] = c.handleRunMission
	c.handlers[protocol.TypeStopMission] = c.handleStopMission
	c.handlers[protocol.TypeResumeMission] = c.handleResumeMission
	c.handlers[protocol.TypeGetMissions] = c.handleGetMissions
	c.handlers[protocol.TypeGetMission] = c.handleGetMission
	c.handlers[protocol.TypeGetTaskDetail] = c.handleGetTaskDetail
	c.handlers[protocol.TypeGetEvents] = c.handleGetEvents
	c.handlers[protocol.TypeChatMessage] = c.handleChatMessage
	c.handlers[protocol.TypeGetChatHistory] = c.handleGetChatHistory
	c.handlers[protocol.TypeGetChatMessages] = c.handleGetChatMessages
	c.handlers[protocol.TypeArchiveChat] = c.handleArchiveChat
	c.handlers[protocol.TypeReloadConfig] = c.handleReloadConfig
	c.handlers[protocol.TypeGetDatasets] = c.handleGetDatasets
	c.handlers[protocol.TypeGetDatasetItems] = c.handleGetDatasetItems
	c.handlers[protocol.TypeListConfigFiles] = c.handleListConfigFiles
	c.handlers[protocol.TypeGetConfigFile] = c.handleGetConfigFile
	c.handlers[protocol.TypeWriteConfigFile] = c.handleWriteConfigFile
	c.handlers[protocol.TypeValidateConfig] = c.handleValidateConfig
	c.handlers[protocol.TypeListSharedFolders] = c.handleListSharedFolders
	c.handlers[protocol.TypeBrowseDirectory] = c.handleBrowseDirectory
	c.handlers[protocol.TypeReadBrowseFile] = c.handleReadBrowseFile
	c.handlers[protocol.TypeWriteBrowseFile] = c.handleWriteBrowseFile
	c.handlers[protocol.TypeDownloadFile] = c.handleDownloadFile
	c.handlers[protocol.TypeDownloadDirectory] = c.handleDownloadDirectory
	c.handlers[protocol.TypeGetVariables] = c.handleGetVariables
	c.handlers[protocol.TypeSetVariable] = c.handleSetVariable
	c.handlers[protocol.TypeDeleteVariable] = c.handleDeleteVariable
}

func (c *Client) handleReloadConfig(env *protocol.Envelope) (*protocol.Envelope, error) {
	if err := c.ReloadConfig(); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeReloadConfigResult, &protocol.ReloadConfigResultPayload{
			Success: false,
			Error:   err.Error(),
		})
	}
	return protocol.NewResponse(env.RequestID, protocol.TypeReloadConfigResult, &protocol.ReloadConfigResultPayload{
		Success: true,
		Config:  ConfigToInstanceConfig(c.getConfig()),
	})
}

func (c *Client) handleGetConfig(env *protocol.Envelope) (*protocol.Envelope, error) {
	instanceConfig := ConfigToInstanceConfig(c.getConfig())
	return protocol.NewResponse(env.RequestID, protocol.TypeGetConfigResult, &protocol.GetConfigResultPayload{
		Config: instanceConfig,
	})
}

func (c *Client) handleRunMission(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.RunMissionPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode run_mission: %w", err)
	}

	// Snapshot config for this mission run
	cfg := c.getConfig()

	// Validate mission exists
	var missionCfg *config.Mission
	for i := range cfg.Missions {
		if cfg.Missions[i].Name == payload.MissionName {
			missionCfg = &cfg.Missions[i]
			break
		}
	}
	if missionCfg == nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   fmt.Sprintf("mission %q not found", payload.MissionName),
		})
	}

	// Create mission runner with no-op debug logger
	debugLogger, _ := mission.NewDebugLogger("")
	runner, err := mission.NewRunner(cfg, c.configPath, payload.MissionName, payload.Inputs, mission.WithDebugLogger(debugLogger))
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   err.Error(),
		})
	}

	// Create WS handler — it will capture the mission ID when MissionStarted fires
	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore())

	// Run mission in background with cancellable context
	missionCtx, missionCancel := context.WithCancel(context.Background())
	go func() {
		c.runMissionChain(missionCtx, missionCancel, runner, storingHandler, wsHandler, payload.MissionName)
	}()

	// Wait for the mission ID (set when Runner calls MissionStarted after creating the mission in the DB)
	missionID, err := wsHandler.WaitForMissionID(30 * time.Second)
	if err != nil {
		missionCancel()
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   fmt.Sprintf("mission failed to start: %v", err),
		})
	}

	// Track the running mission for stop/cancel
	c.missionMu.Lock()
	c.runningMissions[missionID] = missionCancel
	c.missionMu.Unlock()

	return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
		Accepted:  true,
		MissionID: missionID,
	})
}

func (c *Client) handleStopMission(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.StopMissionPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode stop_mission: %w", err)
	}

	c.missionMu.Lock()
	cancel, exists := c.runningMissions[payload.MissionID]
	c.missionMu.Unlock()

	if !exists {
		// Mission not running in this process — update DB status directly for stale runs
		if err := c.stores.Missions.UpdateMissionStatus(payload.MissionID, "stopped"); err != nil {
			return protocol.NewResponse(env.RequestID, protocol.TypeStopMissionAck, &protocol.StopMissionAckPayload{
				Accepted: false,
				Reason:   fmt.Sprintf("mission %q not running and failed to update status: %v", payload.MissionID, err),
			})
		}
		c.emitMissionLifecycleEvent(payload.MissionID, protocol.EventMissionStopped, protocol.MissionStoppedData{
			MissionID: payload.MissionID,
		})
		return protocol.NewResponse(env.RequestID, protocol.TypeStopMissionAck, &protocol.StopMissionAckPayload{
			Accepted: true,
		})
	}

	// Store and emit mission_stopped event BEFORE cancel to avoid racing with store.Close()
	c.emitMissionLifecycleEvent(payload.MissionID, protocol.EventMissionStopped, protocol.MissionStoppedData{
		MissionID: payload.MissionID,
	})

	// Cancel the context — the runner will detect ctx.Done() and clean up
	cancel()

	return protocol.NewResponse(env.RequestID, protocol.TypeStopMissionAck, &protocol.StopMissionAckPayload{
		Accepted: true,
	})
}

func (c *Client) handleResumeMission(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.ResumeMissionPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode resume_mission: %w", err)
	}

	// Snapshot config
	cfg := c.getConfig()

	// Validate mission exists in config
	var missionCfg *config.Mission
	for i := range cfg.Missions {
		if cfg.Missions[i].Name == payload.MissionName {
			missionCfg = &cfg.Missions[i]
			break
		}
	}
	if missionCfg == nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeResumeMissionAck, &protocol.ResumeMissionAckPayload{
			Accepted: false,
			Reason:   fmt.Sprintf("mission %q not found in config", payload.MissionName),
		})
	}

	// Create runner with resume option
	debugLogger, _ := mission.NewDebugLogger("")
	runner, err := mission.NewRunner(cfg, c.configPath, payload.MissionName, nil,
		mission.WithDebugLogger(debugLogger),
		mission.WithResume(payload.MissionID),
	)
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeResumeMissionAck, &protocol.ResumeMissionAckPayload{
			Accepted: false,
			Reason:   err.Error(),
		})
	}

	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore())

	missionCtx, missionCancel := context.WithCancel(context.Background())
	go func() {
		err := runner.Run(missionCtx, storingHandler)

		mid := payload.MissionID
		c.missionMu.Lock()
		delete(c.runningMissions, mid)
		c.missionMu.Unlock()

		if err != nil {
			log.Printf("Resumed mission %q failed: %v", payload.MissionName, err)
			status := "failed"
			if missionCtx.Err() != nil {
				status = "stopped"
			}
			completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
				MissionID: mid,
				Status:    status,
				Error:     err.Error(),
			})
			c.SendEvent(completeEnv)
		} else {
			completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
				MissionID: mid,
				Status:    "completed",
			})
			c.SendEvent(completeEnv)
		}
	}()

	// For resume, the mission ID is already known
	c.missionMu.Lock()
	c.runningMissions[payload.MissionID] = missionCancel
	c.missionMu.Unlock()

	// Still wait for runner to emit MissionStarted (it does so even on resume)
	if _, err := wsHandler.WaitForMissionID(30 * time.Second); err != nil {
		missionCancel()
		return protocol.NewResponse(env.RequestID, protocol.TypeResumeMissionAck, &protocol.ResumeMissionAckPayload{
			Accepted: false,
			Reason:   fmt.Sprintf("mission failed to resume: %v", err),
		})
	}

	// Store and emit mission_resumed event
	c.emitMissionLifecycleEvent(payload.MissionID, protocol.EventMissionResumed, protocol.MissionResumedData{
		MissionID: payload.MissionID,
	})

	return protocol.NewResponse(env.RequestID, protocol.TypeResumeMissionAck, &protocol.ResumeMissionAckPayload{
		Accepted:  true,
		MissionID: payload.MissionID,
	})
}

// emitMissionLifecycleEvent stores a mission-level event in the DB and sends it via WebSocket
// so the commander hub can fan it out to SSE subscribers.
func (c *Client) emitMissionLifecycleEvent(missionID string, eventType protocol.MissionEventType, data interface{}) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		log.Printf("emitMissionLifecycleEvent: marshal: %v", err)
		return
	}

	event := store.MissionEvent{
		ID:        uuid.NewString(),
		MissionID: missionID,
		EventType: string(eventType),
		DataJSON:  string(dataJSON),
		CreatedAt: time.Now(),
	}
	if err := c.stores.Events.StoreEvent(event); err != nil {
		log.Printf("emitMissionLifecycleEvent: store: %v", err)
	}

	// Also send via WebSocket for live SSE
	env, err := protocol.NewEvent(protocol.TypeMissionEvent, &protocol.MissionEventPayload{
		MissionID: missionID,
		EventType: eventType,
		Data:      data,
	})
	if err != nil {
		log.Printf("emitMissionLifecycleEvent: create envelope: %v", err)
		return
	}
	if err := c.SendEvent(env); err != nil {
		log.Printf("emitMissionLifecycleEvent: send: %v", err)
	}
}

func (c *Client) handleGetMissions(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetMissionsPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_missions: %w", err)
	}

	limit := payload.Limit
	if limit <= 0 {
		limit = 50
	}

	records, total, err := c.stores.Missions.ListMissions(limit, payload.Offset)
	if err != nil {
		return nil, fmt.Errorf("list missions: %w", err)
	}

	infos := make([]protocol.MissionRecordInfo, len(records))
	for i, r := range records {
		info := protocol.MissionRecordInfo{
			ID:         r.ID,
			Name:       r.MissionName,
			Status:     r.Status,
			InputsJSON: r.InputValuesJSON,
			StartedAt:  r.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
		if r.FinishedAt != nil {
			s := r.FinishedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			info.FinishedAt = &s
		}
		infos[i] = info
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetMissionsResult, &protocol.GetMissionsResultPayload{
		Missions: infos,
		Total:    total,
	})
}

func (c *Client) handleGetMission(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetMissionPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_mission: %w", err)
	}

	record, err := c.stores.Missions.GetMission(payload.MissionID)
	if err != nil {
		return nil, fmt.Errorf("get mission: %w", err)
	}
	if record == nil {
		return nil, fmt.Errorf("mission %q not found", payload.MissionID)
	}

	missionInfo := protocol.MissionRecordInfo{
		ID:         record.ID,
		Name:       record.MissionName,
		Status:     record.Status,
		InputsJSON: record.InputValuesJSON,
		ConfigJSON: record.ConfigJSON,
		StartedAt:  record.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if record.FinishedAt != nil {
		s := record.FinishedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		missionInfo.FinishedAt = &s
	}

	tasks, err := c.stores.Missions.GetTasksByMission(payload.MissionID)
	if err != nil {
		return nil, fmt.Errorf("get tasks: %w", err)
	}

	taskInfos := make([]protocol.MissionTaskInfo, len(tasks))
	for i, t := range tasks {
		ti := protocol.MissionTaskInfo{
			ID:         t.ID,
			MissionID:  t.MissionID,
			TaskName:   t.TaskName,
			Status:     t.Status,
			ConfigJSON: t.ConfigJSON,
			OutputJSON: t.OutputJSON,
			Error:      t.Error,
		}
		if t.StartedAt != nil {
			s := t.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			ti.StartedAt = &s
		}
		if t.FinishedAt != nil {
			s := t.FinishedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			ti.FinishedAt = &s
		}
		taskInfos[i] = ti
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetMissionResult, &protocol.GetMissionResultPayload{
		Mission: missionInfo,
		Tasks:   taskInfos,
	})
}

func (c *Client) handleGetTaskDetail(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetTaskDetailPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_task_detail: %w", err)
	}

	// Get task record
	taskRecord, err := c.stores.Missions.GetTask(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	taskInfo := protocol.MissionTaskInfo{
		ID:         taskRecord.ID,
		MissionID:  taskRecord.MissionID,
		TaskName:   taskRecord.TaskName,
		Status:     taskRecord.Status,
		ConfigJSON: taskRecord.ConfigJSON,
		OutputJSON: taskRecord.OutputJSON,
		Error:      taskRecord.Error,
	}
	if taskRecord.StartedAt != nil {
		s := taskRecord.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		taskInfo.StartedAt = &s
	}
	if taskRecord.FinishedAt != nil {
		f := taskRecord.FinishedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		taskInfo.FinishedAt = &f
	}

	// Get task outputs
	outputs, err := c.stores.Missions.GetTaskOutputs(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get task outputs: %w", err)
	}
	outputInfos := make([]protocol.TaskOutputInfo, len(outputs))
	for i, o := range outputs {
		outputInfos[i] = protocol.TaskOutputInfo{
			ID:           o.ID,
			TaskID:       o.TaskID,
			DatasetName:  o.DatasetName,
			DatasetIndex: o.DatasetIndex,
			ItemID:       o.ItemID,
			OutputJSON:   o.OutputJSON,
			CreatedAt:    o.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
	}

	// Get sessions
	sessions, err := c.stores.Sessions.GetSessionsByTask(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get sessions: %w", err)
	}
	sessionInfos := make([]protocol.SessionInfoDTO, len(sessions))
	for i, s := range sessions {
		dto := protocol.SessionInfoDTO{
			ID:             s.ID,
			TaskID:         s.TaskID,
			Role:           s.Role,
			AgentName:      s.AgentName,
			Model:          s.Model,
			Status:         s.Status,
			StartedAt:      s.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			IterationIndex: s.IterationIndex,
		}
		if s.FinishedAt != nil {
			f := s.FinishedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			dto.FinishedAt = &f
		}
		sessionInfos[i] = dto
	}

	// Get tool results
	toolResults, err := c.stores.Sessions.GetToolResultsByTask(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get tool results: %w", err)
	}
	toolResultInfos := make([]protocol.ToolResultDTO, len(toolResults))
	for i, tr := range toolResults {
		toolResultInfos[i] = protocol.ToolResultDTO{
			ID:          tr.ID,
			SessionID:   tr.SessionID,
			ToolCallId:  tr.ToolCallId,
			ToolName:    tr.ToolName,
			InputParams: tr.InputParams,
			Output:      tr.RawData,
			StartedAt:   tr.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
			FinishedAt:  tr.FinishedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
	}

	// Get subtasks
	subtasks, err := c.stores.Missions.GetSubtasksByTask(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get subtasks: %w", err)
	}
	subtaskInfos := make([]protocol.SubtaskInfo, len(subtasks))
	for i, st := range subtasks {
		subtaskInfos[i] = protocol.SubtaskInfo{
			Index:          st.Index,
			Title:          st.Title,
			Status:         st.Status,
			SessionID:      st.SessionID,
			IterationIndex: st.IterationIndex,
		}
		if st.CompletedAt != nil {
			f := st.CompletedAt.UTC().Format("2006-01-02T15:04:05.000Z")
			subtaskInfos[i].CompletedAt = &f
		}
	}

	// Get task inputs
	taskInputs, _ := c.stores.Missions.GetTaskInputs(payload.TaskID)
	inputInfos := make([]protocol.TaskInputInfo, len(taskInputs))
	for i, ti := range taskInputs {
		inputInfos[i] = protocol.TaskInputInfo{
			IterationIndex: ti.IterationIndex,
			Objective:      ti.Objective,
		}
	}

	// Get dataset items if the task has an iterator
	var datasetItemInfos []protocol.DatasetItemInfo
	if taskRecord.ConfigJSON != "" {
		var taskCfg struct {
			Iterator *struct {
				Dataset string `json:"dataset"`
			} `json:"iterator,omitempty"`
		}
		if err := json.Unmarshal([]byte(taskRecord.ConfigJSON), &taskCfg); err == nil && taskCfg.Iterator != nil {
			dsID, err := c.stores.Datasets.GetDatasetByName(taskRecord.MissionID, taskCfg.Iterator.Dataset)
			if err == nil {
				count, _ := c.stores.Datasets.GetItemCount(dsID)
				if count > 0 {
					rawItems, err := c.stores.Datasets.GetItemsRaw(dsID, 0, count)
					if err == nil {
						datasetItemInfos = make([]protocol.DatasetItemInfo, len(rawItems))
						for i, raw := range rawItems {
							datasetItemInfos[i] = protocol.DatasetItemInfo{
								Index:    i,
								ItemJSON: raw,
							}
						}
					}
				}
			}
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetTaskDetailResult, &protocol.GetTaskDetailResultPayload{
		Task:         taskInfo,
		Outputs:      outputInfos,
		Sessions:     sessionInfos,
		ToolResults:  toolResultInfos,
		Subtasks:     subtaskInfos,
		Inputs:       inputInfos,
		DatasetItems: datasetItemInfos,
	})
}

func (c *Client) handleChatMessage(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.ChatMessagePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode chat_message: %w", err)
	}

	sessionID := payload.SessionID

	// Look up existing session or create a new one
	c.chatMu.Lock()
	sess, exists := c.chatSessions[sessionID]
	c.chatMu.Unlock()

	if !exists {
		// Snapshot config for this chat session
		cfg := c.getConfig()

		// Validate agent exists in config
		var agentCfg *config.Agent
		for i, ag := range cfg.Agents {
			if ag.Name == payload.AgentName {
				agentCfg = &cfg.Agents[i]
				break
			}
		}
		if agentCfg == nil {
			return protocol.NewResponse(env.RequestID, protocol.TypeChatMessageAck, &protocol.ChatMessageAckPayload{
				SessionID: sessionID,
				Accepted:  false,
				Reason:    fmt.Sprintf("agent %q not found", payload.AgentName),
			})
		}

		a, err := agent.New(context.Background(), agent.Options{
			ConfigPath: c.configPath,
			Config:     cfg,
			AgentName:  payload.AgentName,
		})
		if err != nil {
			return protocol.NewResponse(env.RequestID, protocol.TypeChatMessageAck, &protocol.ChatMessageAckPayload{
				SessionID: sessionID,
				Accepted:  false,
				Reason:    fmt.Sprintf("failed to create agent: %v", err),
			})
		}

		// Persist chat session in store (generates the session ID)
		storeID, storeErr := c.stores.Sessions.CreateChatSession(payload.AgentName, agentCfg.Model)
		if storeErr != nil {
			log.Printf("Failed to create chat session in store: %v", storeErr)
		}
		sessionID = storeID

		// Persist agent system prompts to store
		now := time.Now()
		for _, sp := range a.GetSystemPrompts() {
			c.stores.Sessions.AppendMessage(sessionID, "system", sp, now, now)
		}

		sess = &chatSession{agent: a}
		c.chatMu.Lock()
		c.chatSessions[sessionID] = sess
		c.chatMu.Unlock()
	}

	// Ack immediately — includes the server-generated sessionID
	ackEnv, err := protocol.NewResponse(env.RequestID, protocol.TypeChatMessageAck, &protocol.ChatMessageAckPayload{
		SessionID: sessionID,
		Accepted:  true,
	})
	if err != nil {
		return nil, err
	}

	// Run agent chat in background
	chatHandler := NewWSChatHandler(c, sessionID)
	go func() {
		// Persist user message
		if sessionID != "" {
			now := time.Now()
			c.stores.Sessions.AppendMessage(sessionID, "user", payload.Content, now, now)
		}

		chatStart := time.Now()
		_, chatErr := sess.agent.Chat(context.Background(), payload.Content, chatHandler)

		// Persist assistant answer
		if sessionID != "" {
			if answer := chatHandler.FullAnswer(); answer != "" {
				c.stores.Sessions.AppendMessage(sessionID, "assistant", answer, chatStart, time.Now())
			}
		}

		status := "completed"
		errMsg := ""
		if chatErr != nil {
			status = "error"
			errMsg = chatErr.Error()
			log.Printf("Chat session %s failed: %v", sessionID, chatErr)
		}
		completeEnv, _ := protocol.NewEvent(protocol.TypeChatComplete, &protocol.ChatCompletePayload{
			SessionID: sessionID,
			Status:    status,
			Error:     errMsg,
		})
		c.SendEvent(completeEnv)
	}()

	return ackEnv, nil
}

func (c *Client) handleGetChatHistory(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetChatHistoryPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_chat_history: %w", err)
	}

	limit := payload.Limit
	if limit <= 0 {
		limit = 50
	}

	sessions, total, err := c.stores.Sessions.ListChatSessions(payload.AgentName, limit, payload.Offset)
	if err != nil {
		return nil, fmt.Errorf("list chat sessions: %w", err)
	}

	chats := make([]protocol.ChatSessionInfo, len(sessions))
	for i, s := range sessions {
		chats[i] = protocol.ChatSessionInfo{
			SessionID: s.ID,
			AgentName: s.AgentName,
			Model:     s.Model,
			Status:    s.Status,
			StartedAt: s.StartedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetChatHistoryResult, &protocol.GetChatHistoryResultPayload{
		Chats: chats,
		Total: total,
	})
}

func (c *Client) handleGetChatMessages(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetChatMessagesPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_chat_messages: %w", err)
	}

	msgs, err := c.stores.Sessions.GetMessages(payload.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	var messages []protocol.ChatMessageInfo
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		messages = append(messages, protocol.ChatMessageInfo{
			ID:        m.ID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetChatMessagesResult, &protocol.GetChatMessagesResultPayload{
		Messages: messages,
	})
}

func (c *Client) handleArchiveChat(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.ArchiveChatPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode archive_chat: %w", err)
	}

	c.stores.Sessions.CompleteSession(payload.SessionID, nil)

	// Remove from active chat sessions if present
	c.chatMu.Lock()
	delete(c.chatSessions, payload.SessionID)
	c.chatMu.Unlock()

	return protocol.NewResponse(env.RequestID, protocol.TypeArchiveChatAck, &protocol.ArchiveChatAckPayload{
		Accepted: true,
	})
}

func (c *Client) handleGetEvents(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetEventsPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_events: %w", err)
	}

	limit := payload.Limit
	if limit <= 0 {
		limit = 100
	}

	events, err := c.stores.Events.GetEventsByMission(payload.MissionID, limit, payload.Offset)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}

	eventInfos := make([]protocol.MissionEventInfo, len(events))
	for i, e := range events {
		eventInfos[i] = protocol.MissionEventInfo{
			ID:             e.ID,
			MissionID:      e.MissionID,
			TaskID:         e.TaskID,
			SessionID:      e.SessionID,
			IterationIndex: e.IterationIndex,
			EventType:      e.EventType,
			DataJSON:       e.DataJSON,
			CreatedAt:      e.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetEventsResult, &protocol.GetEventsResultPayload{
		Events: eventInfos,
	})
}

func (c *Client) handleGetDatasets(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetDatasetsPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_datasets: %w", err)
	}

	datasets, err := c.stores.Datasets.ListDatasets(payload.MissionID)
	if err != nil {
		return nil, fmt.Errorf("list datasets: %w", err)
	}

	infos := make([]protocol.DatasetRecordInfo, len(datasets))
	for i, d := range datasets {
		infos[i] = protocol.DatasetRecordInfo{
			ID:          d.ID,
			Name:        d.Name,
			Description: d.Description,
			ItemCount:   d.ItemCount,
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetDatasetsResult, &protocol.GetDatasetsResultPayload{
		Datasets: infos,
	})
}

func (c *Client) handleGetDatasetItems(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetDatasetItemsPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_dataset_items: %w", err)
	}

	limit := payload.Limit
	if limit <= 0 {
		limit = 50
	}

	items, err := c.stores.Datasets.GetItemsRaw(payload.DatasetID, payload.Offset, limit)
	if err != nil {
		return nil, fmt.Errorf("get dataset items: %w", err)
	}

	total, err := c.stores.Datasets.GetItemCount(payload.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("get item count: %w", err)
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetDatasetItemsResult, &protocol.GetDatasetItemsResultPayload{
		Items: items,
		Total: total,
	})
}

func (c *Client) handleValidateConfig(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.ValidateConfigPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode validate_config: %w", err)
	}

	// Create temp dir and copy all config files into it
	tmpDir, err := os.MkdirTemp("", "squadron-validate-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Resolve config directory
	info, err := os.Stat(c.configPath)
	if err != nil {
		return nil, fmt.Errorf("stat config path: %w", err)
	}

	var configDir string
	if info.IsDir() {
		configDir = c.configPath
	} else {
		configDir = filepath.Dir(c.configPath)
	}

	// Copy existing .hcl files to temp dir (recursively)
	filepath.WalkDir(configDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".hcl") {
			rel, err := filepath.Rel(configDir, path)
			if err != nil {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			destPath := filepath.Join(tmpDir, rel)
			os.MkdirAll(filepath.Dir(destPath), 0755)
			os.WriteFile(destPath, content, 0644)
		}
		return nil
	})

	// Overlay modified files from the payload
	for name, content := range payload.Files {
		if err := validateConfigPath(name); err != nil {
			return protocol.NewResponse(env.RequestID, protocol.TypeValidateConfigResult, &protocol.ValidateConfigResultPayload{
				Valid:  false,
				Errors: []string{fmt.Sprintf("invalid filename %q: %v", name, err)},
			})
		}
		destPath := filepath.Join(tmpDir, name)
		os.MkdirAll(filepath.Dir(destPath), 0755)
		os.WriteFile(destPath, []byte(content), 0644)
	}

	// Validate by loading the full config from the temp dir
	_, loadErr := config.LoadAndValidate(tmpDir)
	if loadErr != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeValidateConfigResult, &protocol.ValidateConfigResultPayload{
			Valid:  false,
			Errors: []string{loadErr.Error()},
		})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeValidateConfigResult, &protocol.ValidateConfigResultPayload{
		Valid: true,
	})
}

// validateConfigPath checks that a file path is safe (no path traversal, stays within config dir).
func validateConfigPath(name string) error {
	if name == "" {
		return fmt.Errorf("filename is required")
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("invalid filename")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("invalid filename")
	}
	cleaned := filepath.Clean(name)
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return fmt.Errorf("invalid filename")
	}
	return nil
}

func (c *Client) handleListConfigFiles(env *protocol.Envelope) (*protocol.Envelope, error) {
	info, err := os.Stat(c.configPath)
	if err != nil {
		return nil, fmt.Errorf("stat config path: %w", err)
	}

	files := []protocol.ConfigFileInfo{}
	var configDir string

	if info.IsDir() {
		configDir = c.configPath
		filepath.WalkDir(configDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			// Skip hidden directories (but not "." itself)
			if d.IsDir() && d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(configDir, path)
			if err != nil {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			files = append(files, protocol.ConfigFileInfo{
				Name: rel,
				Size: fi.Size(),
			})
			return nil
		})
	} else {
		configDir = filepath.Dir(c.configPath)
		fi, err := os.Stat(c.configPath)
		if err != nil {
			return nil, fmt.Errorf("stat config file: %w", err)
		}
		files = append(files, protocol.ConfigFileInfo{
			Name: filepath.Base(c.configPath),
			Size: fi.Size(),
		})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeListConfigFilesResult, &protocol.ListConfigFilesResultPayload{
		Files: files,
		Path:  configDir,
	})
}

func (c *Client) handleGetConfigFile(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetConfigFilePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_config_file: %w", err)
	}

	if err := validateConfigPath(payload.Name); err != nil {
		return nil, err
	}

	// Resolve the config directory
	info, _ := os.Stat(c.configPath)
	var dir string
	if info != nil && info.IsDir() {
		dir = c.configPath
	} else {
		dir = filepath.Dir(c.configPath)
	}

	content, err := os.ReadFile(filepath.Join(dir, payload.Name))
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetConfigFileResult, &protocol.GetConfigFileResultPayload{
		Name:    payload.Name,
		Content: string(content),
	})
}

func (c *Client) handleWriteConfigFile(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.WriteConfigFilePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode write_config_file: %w", err)
	}

	if err := validateConfigPath(payload.Name); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteConfigFileResult, &protocol.WriteConfigFileResultPayload{
			Success: false,
			Error:   err.Error(),
		})
	}

	// Resolve the config directory
	info, _ := os.Stat(c.configPath)
	var dir string
	if info != nil && info.IsDir() {
		dir = c.configPath
	} else {
		dir = filepath.Dir(c.configPath)
	}

	filePath := filepath.Join(dir, payload.Name)
	if err := os.WriteFile(filePath, []byte(payload.Content), 0644); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteConfigFileResult, &protocol.WriteConfigFileResultPayload{
			Success: false,
			Error:   err.Error(),
		})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeWriteConfigFileResult, &protocol.WriteConfigFileResultPayload{
		Success: true,
	})
}

// =============================================================================
// Variable operations
// =============================================================================

func maskSecret(value string) string {
	if len(value) <= 4 {
		return strings.Repeat("•", len(value))
	}
	return value[:4] + "••••"
}

func (c *Client) handleGetVariables(env *protocol.Envelope) (*protocol.Envelope, error) {
	cfg := c.getConfig()

	fileVars, err := config.LoadVarsFromFile()
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeError, &protocol.ErrorPayload{
			Code:    "vars_load_error",
			Message: err.Error(),
		})
	}

	var cfgVars []config.Variable
	if cfg != nil {
		cfgVars = cfg.Variables
	}
	details := make([]protocol.VariableDetail, 0, len(cfgVars))
	for _, v := range cfgVars {
		detail := protocol.VariableDetail{
			Name:   v.Name,
			Secret: v.Secret,
		}

		if fileVal, ok := fileVars[v.Name]; ok {
			detail.HasValue = true
			detail.Source = "override"
			if v.Secret {
				detail.Value = maskSecret(fileVal)
			} else {
				detail.Value = fileVal
			}
		} else if v.Default != "" {
			detail.HasValue = true
			detail.Source = "default"
			detail.Default = v.Default
			if v.Secret {
				detail.Value = maskSecret(v.Default)
			} else {
				detail.Value = v.Default
			}
		} else {
			detail.Source = "unset"
		}

		details = append(details, detail)
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetVariablesResult, &protocol.GetVariablesResultPayload{
		Variables: details,
	})
}

func (c *Client) handleSetVariable(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.SetVariablePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeSetVariableResult, &protocol.SetVariableResultPayload{
			Error: "invalid payload: " + err.Error(),
		})
	}

	// Validate variable exists in config (skip if config not yet loaded)
	cfg := c.getConfig()
	if cfg != nil {
		found := false
		for _, v := range cfg.Variables {
			if v.Name == payload.Name {
				found = true
				break
			}
		}
		if !found {
			return protocol.NewResponse(env.RequestID, protocol.TypeSetVariableResult, &protocol.SetVariableResultPayload{
				Error: fmt.Sprintf("variable %q not defined in config", payload.Name),
			})
		}
	}

	if err := config.SetVar(payload.Name, payload.Value); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeSetVariableResult, &protocol.SetVariableResultPayload{
			Error: err.Error(),
		})
	}

	_ = c.ReloadConfig()

	return protocol.NewResponse(env.RequestID, protocol.TypeSetVariableResult, &protocol.SetVariableResultPayload{
		Success: true,
	})
}

func (c *Client) handleDeleteVariable(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.DeleteVariablePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeDeleteVariableResult, &protocol.DeleteVariableResultPayload{
			Error: "invalid payload: " + err.Error(),
		})
	}

	if err := config.DeleteVar(payload.Name); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeDeleteVariableResult, &protocol.DeleteVariableResultPayload{
			Error: err.Error(),
		})
	}

	_ = c.ReloadConfig()

	return protocol.NewResponse(env.RequestID, protocol.TypeDeleteVariableResult, &protocol.DeleteVariableResultPayload{
		Success: true,
	})
}

// runMissionChain runs a mission and, if it routes to another mission, chains into that mission.
func (c *Client) runMissionChain(ctx context.Context, cancel context.CancelFunc, runner *mission.Runner, storingHandler *streamers.StoringMissionHandler, wsHandler *WSMissionHandler, missionName string) {
	for {
		err := runner.Run(ctx, storingHandler)

		mid := wsHandler.MissionID()
		c.missionMu.Lock()
		delete(c.runningMissions, mid)
		c.missionMu.Unlock()

		if err != nil {
			log.Printf("Mission %q failed: %v", missionName, err)
			status := "failed"
			if ctx.Err() != nil {
				status = "stopped"
			}
			completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
				MissionID: mid,
				Status:    status,
				Error:     err.Error(),
			})
			c.SendEvent(completeEnv)
			return
		}

		// Send completion for this mission
		completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
			MissionID: mid,
			Status:    "completed",
		})
		c.SendEvent(completeEnv)

		// Check for cross-mission routing
		nextMission := runner.NextMission()
		if nextMission == "" {
			return
		}

		// Chain into the next mission
		log.Printf("Mission %q routed to mission %q", missionName, nextMission)
		missionName = nextMission
		inputs := runner.NextMissionInputs()

		cfg := c.getConfig()
		debugLogger, _ := mission.NewDebugLogger("")
		var newErr error
		runner, newErr = mission.NewRunner(cfg, c.configPath, nextMission, inputs, mission.WithDebugLogger(debugLogger))
		if newErr != nil {
			log.Printf("Failed to create runner for chained mission %q: %v", nextMission, newErr)
			return
		}

		wsHandler = NewWSMissionHandler(c)
		storingHandler = streamers.NewStoringMissionHandler(wsHandler, runner.EventStore())

		// Wait for mission ID in a separate goroutine — we need it to track the running mission
		go func() {
			chainedMID, err := wsHandler.WaitForMissionID(30 * time.Second)
			if err != nil {
				log.Printf("Chained mission %q failed to start: %v", nextMission, err)
				return
			}
			c.missionMu.Lock()
			c.runningMissions[chainedMID] = cancel
			c.missionMu.Unlock()
		}()
	}
}
