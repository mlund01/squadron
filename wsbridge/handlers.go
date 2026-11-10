package wsbridge

import (
	"context"
	"encoding/json"
	"errors"
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
	"squadron/config/runtimemodels"
	"squadron/mission"
	missionruntime "squadron/mission/runtime"
	"squadron/notification"
	"squadron/store"
	"squadron/streamers"
)

func (c *Client) registerHandlers() {
	c.handlers["agent_conversation"] = c.handleAgentConversation
	c.handlers["get_agent_definition"] = c.handleAgentDefinition
	c.handlers["get_mission_definition"] = c.handleMissionDefinition
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
	c.handlers[typeListLocalPluginFiles] = c.handleListLocalPluginFiles
	c.handlers[typeGetLocalPluginFile] = c.handleGetLocalPluginFile
	c.handlers[protocol.TypeWriteConfigFile] = c.handleWriteConfigFile
	c.handlers[protocol.TypeValidateConfig] = c.handleValidateConfig
	c.handlers[protocol.TypeListSharedFolders] = c.handleListSharedFolders
	c.handlers[protocol.TypeBrowseDirectory] = c.handleBrowseDirectory
	c.handlers[protocol.TypeReadBrowseFile] = c.handleReadBrowseFile
	c.handlers[protocol.TypeWriteBrowseFile] = c.handleWriteBrowseFile
	c.handlers[protocol.TypeDownloadFile] = c.handleDownloadFile
	c.handlers[protocol.TypeDownloadDirectory] = c.handleDownloadDirectory
	c.handlers[protocol.TypeGetCostSummary] = c.handleGetCostSummary
	c.handlers[protocol.TypeSubscribe] = c.handleSubscribe
	c.handlers[protocol.TypeUnsubscribe] = c.handleUnsubscribe
	c.handlers[protocol.TypeGetHumanInputs] = c.handleGetHumanInputs
	c.handlers[protocol.TypeResolveHumanInput] = c.handleResolveHumanInput
	c.handlers["sync_variables"] = c.handleSyncVariables
	c.handlers["sync_model_connections"] = c.handleSyncModelConnections
}

func isMissionWaiting(err error) bool {
	var human *missionruntime.MissionHumanInputWaitingError
	var recovery *missionruntime.MissionRecoveryWaitingError
	var retry *missionruntime.MissionRetryWaitingError
	return errors.As(err, &human) || errors.As(err, &recovery) || errors.As(err, &retry)
}

type syncModelConnectionsPayload struct {
	Connections map[string]runtimemodels.Connection `json:"connections"`
}

func (c *Client) handleSyncModelConnections(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload syncModelConnectionsPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode sync_model_connections: %w", err)
	}
	runtimemodels.Replace(payload.Connections)
	if err := c.ReloadConfig(); err != nil {
		c.cfgMu.Lock()
		c.cfgReady = false
		c.cfgError = err.Error()
		c.cfgMu.Unlock()
		c.NotifyConfigReloaded(err)
		return nil, nil
	}
	c.NotifyConfigReloaded(nil)
	return nil, nil
}

type syncVariablesPayload struct {
	Values map[string]string `json:"values"`
}

func (c *Client) handleSyncVariables(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload syncVariablesPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode sync_variables: %w", err)
	}

	config.ReplaceRuntimeVars(payload.Values)
	if err := c.ReloadConfig(); err != nil {
		c.cfgMu.Lock()
		c.cfgReady = false
		c.cfgError = err.Error()
		c.cfgMu.Unlock()
		c.NotifyConfigReloaded(err)
		return nil, nil
	}
	c.NotifyConfigReloaded(nil)
	return nil, nil
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
	var payload struct {
		protocol.RunMissionPayload
		ThreadID string `json:"threadId,omitempty"`
	}
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

	// Check concurrency limit
	if !c.concurrency.NotifyMissionStarted(payload.MissionName) {
		reason := fmt.Sprintf("mission %q is at max parallel capacity (%d)", payload.MissionName, missionCfg.MaxParallel)
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   reason,
		})
	}

	// Create mission runner with no-op debug logger
	debugLogger, _ := mission.NewDebugLogger("")
	runner, err := mission.NewRunner(cfg, c.configPath, payload.MissionName, payload.Inputs, mission.WithThreadID(payload.ThreadID), mission.WithDebugLogger(debugLogger), mission.WithHumanBridge(c), mission.WithGatewayBridge(c.gatewayBridge))
	if err != nil {
		c.concurrency.NotifyMissionDone(payload.MissionName)
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   err.Error(),
		})
	}

	// Create WS handler — it will capture the mission ID when MissionStarted fires
	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore(), runner.CostStore())

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
	c.runningMissions[missionID] = &runningMission{cancel: missionCancel, drain: runner.Drain}
	c.missionMu.Unlock()

	return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &struct {
		protocol.RunMissionAckPayload
		ThreadID string `json:"threadId"`
	}{protocol.RunMissionAckPayload{Accepted: true, MissionID: missionID}, runner.ThreadID()})
}

func (c *Client) handleStopMission(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.StopMissionPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode stop_mission: %w", err)
	}

	c.missionMu.Lock()
	rm, exists := c.runningMissions[payload.MissionID]
	c.missionMu.Unlock()

	if !exists {
		// A human-input wait has no live worker. Stop its durable runtime
		// before changing the directory status, then close the open prompts.
		if record, err := c.stores.Missions.GetMission(payload.MissionID); err == nil && record.Status == "waiting" {
			harness, _, restoreErr := missionruntime.RestoreDurableHarness(c.stores.Runtime, payload.MissionID)
			if restoreErr != nil {
				return nil, fmt.Errorf("restore waiting mission before stop: %w", restoreErr)
			}
			if stopErr := harness.Stop(); stopErr != nil {
				return nil, fmt.Errorf("stop waiting mission: %w", stopErr)
			}
		}
		// Mission not running in this process — update DB status directly.
		if err := c.stores.Missions.UpdateMissionStatus(payload.MissionID, "stopped"); err != nil {
			return protocol.NewResponse(env.RequestID, protocol.TypeStopMissionAck, &protocol.StopMissionAckPayload{
				Accepted: false,
				Reason:   fmt.Sprintf("mission %q not running and failed to update status: %v", payload.MissionID, err),
			})
		}
		c.cancelOpenHumanInputsForMission(payload.MissionID, "[cancelled: mission stopped]")
		c.emitMissionLifecycleEvent(payload.MissionID, protocol.EventMissionStopped, protocol.MissionStoppedData{
			MissionID: payload.MissionID,
		})
		return protocol.NewResponse(env.RequestID, protocol.TypeStopMissionAck, &protocol.StopMissionAckPayload{
			Accepted: true,
		})
	}

	// Emit mission_stopped event
	c.emitMissionLifecycleEvent(payload.MissionID, protocol.EventMissionStopped, protocol.MissionStoppedData{
		MissionID: payload.MissionID,
	})

	// Drain first (graceful), then cancel context as hard backstop
	if rm.drain != nil {
		rm.drain()
	}
	rm.cancel()

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
		mission.WithHumanBridge(c),
		mission.WithGatewayBridge(c.gatewayBridge))
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeResumeMissionAck, &protocol.ResumeMissionAckPayload{
			Accepted: false,
			Reason:   err.Error(),
		})
	}

	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore(), runner.CostStore())

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
			} else if isMissionWaiting(err) {
				status = "waiting"
			}
			if status != "waiting" {
				c.cancelOpenHumanInputsForMission(mid, "[cancelled: mission "+status+"]")
			} else {
				c.scheduleWaitingHumanInputMission(mid)
				go c.resumeReadyHumanInputMission(mid)
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

		runner.CloseStores()
	}()

	// For resume, the mission ID is already known
	c.missionMu.Lock()
	c.runningMissions[payload.MissionID] = &runningMission{cancel: missionCancel, drain: runner.Drain}
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
			Summary:    t.Summary,
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
	runtimeInfo, _, err := missionRuntimeProjection(c.stores.Runtime, payload.MissionID)
	if err != nil {
		return nil, err
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetMissionResult, &protocol.GetMissionResultPayload{
		Mission: missionInfo,
		Tasks:   taskInfos,
		Runtime: runtimeInfo,
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
		Summary:    taskRecord.Summary,
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

	// Rebuild the explicit tool-call -> media association from structured
	// session parts. Media is intentionally not inferred from event timing or
	// neighboring assistant messages.
	toolMedia := make(map[string][]protocol.ToolMediaDTO)
	for _, session := range sessions {
		messages, err := c.stores.Sessions.GetStructuredMessages(session.ID)
		if err != nil {
			log.Printf("[wsbridge] structured messages unavailable for session %s: %v", session.ID, err)
			continue
		}
		for _, message := range messages {
			for _, part := range message.Parts {
				var media protocol.ToolMediaDTO
				switch part.Type {
				case store.PartTypeToolImage:
					media = protocol.ToolMediaDTO{Kind: "image", MediaType: part.ImageMediaType, Data: part.ImageData}
				case store.PartTypeToolDocument:
					media = protocol.ToolMediaDTO{Kind: "document", MediaType: part.DocumentMediaType, Data: part.DocumentData, Filename: part.DocumentFilename}
				default:
					continue
				}
				if part.ToolUseID != "" {
					toolMedia[session.ID+"\x00"+part.ToolUseID] = append(toolMedia[session.ID+"\x00"+part.ToolUseID], media)
				}
			}
		}
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
			Media:       toolMedia[tr.SessionID+"\x00"+tr.ToolCallId],
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
	if strings.HasPrefix(payload.AgentName, "cc_conversation:") {
		return nil, fmt.Errorf("use the scoped conversation API")
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
	if len(msgs) > 0 && isConversationMetadata(msgs[0].Content) {
		return nil, fmt.Errorf("use the scoped conversation API")
	}

	var messages []protocol.ChatMessageInfo
	for _, m := range msgs {
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
	msgs, err := c.stores.Sessions.GetMessages(payload.SessionID)
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 && isConversationMetadata(msgs[0].Content) {
		return nil, fmt.Errorf("use the scoped conversation API")
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
	runtimeInfo, runtimeEvents, err := missionRuntimeProjection(c.stores.Runtime, payload.MissionID)
	if err != nil {
		return nil, err
	}
	if runtimeInfo != nil {
		start := payload.Offset
		if start < 0 {
			start = 0
		}
		if start > len(runtimeEvents) {
			start = len(runtimeEvents)
		}
		end := start + limit
		if end > len(runtimeEvents) {
			end = len(runtimeEvents)
		}
		infos := make([]protocol.MissionEventInfo, 0, end-start)
		for _, event := range runtimeEvents[start:end] {
			infos = append(infos, protocol.MissionEventInfo{
				ID: fmt.Sprintf("runtime:%d", event.Sequence), MissionID: payload.MissionID,
				EventType: event.EventType, DataJSON: event.PayloadJSON,
				CreatedAt: event.OccurredAt.UTC().Format(time.RFC3339Nano),
			})
		}
		return protocol.NewResponse(env.RequestID, protocol.TypeGetEventsResult, &protocol.GetEventsResultPayload{Events: infos})
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

	copyConfigTree(configDir, tmpDir)

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

// copyConfigTree copies .hcl, .md, and .txt files from src into dst, preserving
// relative paths. .md and .txt are included because load() can reference them
// from HCL. Dot-directories are skipped. Errors on individual files are ignored
// so a single unreadable file doesn't abort validation.
func copyConfigTree(src, dst string) {
	filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".hcl") && !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".txt") {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		destPath := filepath.Join(dst, rel)
		os.MkdirAll(filepath.Dir(destPath), 0755)
		os.WriteFile(destPath, content, 0644)
		return nil
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

// runMissionChain runs a mission and, if it routes to another mission, chains into that mission.
func (c *Client) runMissionChain(ctx context.Context, cancel context.CancelFunc, runner *mission.Runner, storingHandler *streamers.StoringMissionHandler, wsHandler *WSMissionHandler, missionName string) {
	for {
		err := runner.Run(ctx, storingHandler)

		mid := wsHandler.MissionID()
		c.missionMu.Lock()
		delete(c.runningMissions, mid)
		c.missionMu.Unlock()
		c.concurrency.NotifyMissionDone(missionName)

		if err != nil {
			log.Printf("Mission %q stopped execution: %v", missionName, err)
			status := "failed"
			if ctx.Err() != nil {
				status = "stopped"
			} else if isMissionWaiting(err) {
				status = "waiting"
			}
			if status != "waiting" {
				c.cancelOpenHumanInputsForMission(mid, "[cancelled: mission "+status+"]")
			} else {
				c.scheduleWaitingHumanInputMission(mid)
				go c.resumeReadyHumanInputMission(mid)
			}
			completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
				MissionID: mid,
				Status:    status,
				Error:     err.Error(),
			})
			c.SendEvent(completeEnv)
			// Only mission_failed is a notification event; a user-initiated
			// stop does not fire one.
			if status == "failed" {
				c.dispatchNotification(runner.NotificationConfig(), notification.Record{
					ThreadID:    runner.ThreadID(),
					MissionID:   mid,
					MissionName: missionName,
					Event:       config.NotifyMissionFailed,
					Title:       "Mission \"" + missionName + "\" failed",
					OccurredAt:  time.Now(),
					Error:       err.Error(),
				})
			}
			runner.CloseStores()
			return
		}

		// Send completion for this mission
		completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
			MissionID: mid,
			Status:    "completed",
		})
		c.SendEvent(completeEnv)
		c.dispatchNotification(runner.NotificationConfig(), notification.Record{
			ThreadID:    runner.ThreadID(),
			MissionID:   mid,
			MissionName: missionName,
			Event:       config.NotifyMissionCompleted,
			Title:       "Mission \"" + missionName + "\" completed",
			OccurredAt:  time.Now(),
		})

		// Check for cross-mission routing
		nextMission := runner.NextMission()
		if nextMission == "" {
			runner.CloseStores()
			return
		}

		// Keep the parent store open until the child has a persisted run ID. The
		// child uses a deterministic identity derived from this durable request,
		// so retrying after a crash cannot create a duplicate run.
		parentRunner := runner
		parentMissionID := parentRunner.MissionID()
		launchRequestID := parentRunner.NextMissionLaunchID()
		inputs := parentRunner.NextMissionInputs()

		// Chain into the next mission.
		log.Printf("Mission %q routed to mission %q", missionName, nextMission)
		missionName = nextMission
		if !c.concurrency.NotifyMissionStarted(nextMission) {
			log.Printf("Mission %q launch %q is waiting for concurrency capacity", nextMission, launchRequestID)
			parentRunner.CloseStores()
			return
		}

		cfg := c.getConfig()
		debugLogger, _ := mission.NewDebugLogger("")
		var newErr error
		options := []mission.RunnerOption{mission.WithThreadID(parentRunner.ThreadID()), mission.WithDebugLogger(debugLogger), mission.WithHumanBridge(c), mission.WithGatewayBridge(c.gatewayBridge)}
		if launchRequestID != "" {
			options = append(options, mission.WithCrossMissionLaunch(parentMissionID, launchRequestID))
		}
		runner, newErr = mission.NewRunner(cfg, c.configPath, nextMission, inputs, options...)
		if newErr != nil {
			log.Printf("Failed to create runner for chained mission %q: %v", nextMission, newErr)
			c.concurrency.NotifyMissionDone(nextMission)
			parentRunner.CloseStores()
			return
		}

		wsHandler = NewWSMissionHandler(c)
		storingHandler = streamers.NewStoringMissionHandler(wsHandler, runner.EventStore(), runner.CostStore())

		// Wait for mission ID in a separate goroutine — we need it to track the running mission
		childRunner := runner
		childHandler := wsHandler
		go func(parentRunner, childRunner *mission.Runner, childHandler *WSMissionHandler, parentMissionID, launchRequestID, nextMission string) {
			chainedMID, err := childHandler.WaitForMissionID(30 * time.Second)
			if err != nil {
				log.Printf("Chained mission %q failed to start: %v", nextMission, err)
				parentRunner.CloseStores()
				return
			}
			if launchRequestID != "" {
				if err := parentRunner.AcknowledgeNextMissionLaunch(chainedMID); err != nil {
					log.Printf("Failed to acknowledge launch %q from mission %q to child %q: %v", launchRequestID, parentMissionID, chainedMID, err)
				} else {
					log.Printf("Acknowledged launch %q from mission %q to child %q", launchRequestID, parentMissionID, chainedMID)
				}
			}
			parentRunner.CloseStores()
			c.missionMu.Lock()
			c.runningMissions[chainedMID] = &runningMission{cancel: cancel, drain: childRunner.Drain}
			c.missionMu.Unlock()
		}(parentRunner, childRunner, childHandler, parentMissionID, launchRequestID, nextMission)
	}
}

// ResumeOrphanedMissions finds missions stuck in "running" status (from a crash/kill)
// and auto-resumes them. Called once on startup after config is loaded.
func (c *Client) ResumeOrphanedMissions() {
	cfg := c.getConfig()
	if cfg == nil {
		return
	}

	var records []store.MissionRecord
	for offset := 0; ; offset += 100 {
		page, total, err := c.stores.Missions.ListMissions(100, offset)
		if err != nil {
			log.Printf("auto-resume: failed to list missions: %v", err)
			return
		}
		records = append(records, page...)
		if len(records) >= total || len(page) == 0 {
			break
		}
	}

	type pendingLaunch struct {
		parentRunID string
		launch      missionruntime.CrossMissionLaunch
	}
	pendingByChild := make(map[string]pendingLaunch)
	for _, record := range records {
		if record.Status != "completed" {
			continue
		}
		harness, _, err := missionruntime.RestoreDurableHarness(c.stores.Runtime, record.ID)
		if err != nil {
			continue
		}
		for _, launch := range harness.Snapshot().CrossMissionLaunches {
			if launch.State != missionruntime.CrossMissionLaunchPending {
				continue
			}
			childID := store.MissionIDForLaunch(record.ID, launch.ID)
			pendingByChild[childID] = pendingLaunch{parentRunID: record.ID, launch: launch}
		}
	}

	childrenWithoutRuntime := make(map[string]bool)
	for _, record := range records {
		pending, ok := pendingByChild[record.ID]
		if !ok {
			continue
		}
		if _, _, err := missionruntime.RestoreDurableHarness(c.stores.Runtime, record.ID); err != nil {
			childrenWithoutRuntime[record.ID] = true
			continue
		}
		if err := c.acknowledgeRecoveredLaunch(pending.parentRunID, pending.launch.ID, record.ID); err != nil {
			log.Printf("auto-resume: failed to acknowledge recovered child %q: %v", record.ID, err)
			continue
		}
		delete(pendingByChild, record.ID)
	}

	for _, r := range records {
		if childrenWithoutRuntime[r.ID] {
			continue
		}
		if r.Status == "waiting" {
			c.scheduleWaitingHumanInputMission(r.ID)
			go c.resumeReadyHumanInputMission(r.ID)
			continue
		}
		if r.Status != "running" {
			continue
		}

		// Check if mission still exists in config
		var found bool
		for _, m := range cfg.Missions {
			if m.Name == r.MissionName {
				found = true
				break
			}
		}
		if !found {
			log.Printf("auto-resume: mission %q (%s) not in config, marking as stopped", r.MissionName, r.ID)
			c.stores.Missions.UpdateMissionStatus(r.ID, "stopped")
			continue
		}

		// Mark as stopped first (resume expects stopped state)
		c.stores.Missions.UpdateMissionStatus(r.ID, "stopped")

		log.Printf("auto-resume: resuming orphaned mission %q (%s)", r.MissionName, r.ID)

		debugLogger, _ := mission.NewDebugLogger("")
		runner, err := mission.NewRunner(cfg, c.configPath, r.MissionName, nil,
			mission.WithDebugLogger(debugLogger),
			mission.WithResume(r.ID),
			mission.WithAutomaticRecovery(),
			mission.WithHumanBridge(c),
			mission.WithGatewayBridge(c.gatewayBridge))
		if err != nil {
			log.Printf("auto-resume: failed to create runner for %q: %v", r.MissionName, err)
			continue
		}

		wsHandler := NewWSMissionHandler(c)
		storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore(), runner.CostStore())

		missionCtx, missionCancel := context.WithCancel(context.Background())
		go func(missionName, missionID string) {
			c.runMissionChain(missionCtx, missionCancel, runner, storingHandler, wsHandler, missionName)
		}(r.MissionName, r.ID)

		// Track the running mission
		c.missionMu.Lock()
		c.runningMissions[r.ID] = &runningMission{cancel: missionCancel, drain: runner.Drain}
		c.missionMu.Unlock()
	}

	for _, pending := range pendingByChild {
		c.resumePendingCrossMissionLaunch(cfg, pending.parentRunID, pending.launch)
	}
}

func (c *Client) acknowledgeRecoveredLaunch(parentRunID, launchID, childRunID string) error {
	harness, _, err := missionruntime.RestoreDurableHarness(c.stores.Runtime, parentRunID)
	if err != nil {
		return err
	}
	_, err = harness.CompleteCrossMissionLaunch(launchID, childRunID)
	return err
}

func (c *Client) resumePendingCrossMissionLaunch(cfg *config.Config, parentRunID string, launch missionruntime.CrossMissionLaunch) {
	if !c.concurrency.NotifyMissionStarted(launch.Target) {
		log.Printf("auto-resume: pending launch %q is waiting for capacity on mission %q", launch.ID, launch.Target)
		return
	}
	debugLogger, _ := mission.NewDebugLogger("")
	runner, err := mission.NewRunner(cfg, c.configPath, launch.Target, launch.Inputs,
		mission.WithDebugLogger(debugLogger),
		mission.WithCrossMissionLaunch(parentRunID, launch.ID),
		mission.WithHumanBridge(c),
		mission.WithGatewayBridge(c.gatewayBridge))
	if err != nil {
		c.concurrency.NotifyMissionDone(launch.Target)
		log.Printf("auto-resume: failed to rebuild pending launch %q: %v", launch.ID, err)
		return
	}
	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore(), runner.CostStore())
	missionCtx, missionCancel := context.WithCancel(context.Background())
	go c.runMissionChain(missionCtx, missionCancel, runner, storingHandler, wsHandler, launch.Target)

	go func() {
		childID, err := wsHandler.WaitForMissionID(30 * time.Second)
		if err != nil {
			missionCancel()
			log.Printf("auto-resume: pending launch %q failed to start: %v", launch.ID, err)
			return
		}
		if err := c.acknowledgeRecoveredLaunch(parentRunID, launch.ID, childID); err != nil {
			log.Printf("auto-resume: failed to acknowledge launch %q to child %q: %v", launch.ID, childID, err)
		}
		c.missionMu.Lock()
		c.runningMissions[childID] = &runningMission{cancel: missionCancel, drain: runner.Drain}
		c.missionMu.Unlock()
	}()
}

// RunMissionDirect starts a mission without going through the WebSocket protocol.
// It returns the run ID and mission thread ID after startup. An empty
// threadID starts a new mission thread; execution continues in the background.
func (c *Client) RunMissionDirect(missionName string, inputs map[string]string, threadID string) (string, string, error) {
	cfg := c.getConfig()
	if cfg == nil {
		return "", "", fmt.Errorf("config not loaded")
	}

	// Validate mission exists
	var missionCfg *config.Mission
	for i := range cfg.Missions {
		if cfg.Missions[i].Name == missionName {
			missionCfg = &cfg.Missions[i]
			break
		}
	}
	if missionCfg == nil {
		return "", "", fmt.Errorf("mission %q not found", missionName)
	}

	// Check concurrency limit
	if !c.concurrency.NotifyMissionStarted(missionName) {
		return "", "", fmt.Errorf("mission %q is at max parallel capacity (%d)", missionName, missionCfg.MaxParallel)
	}

	// Create mission runner
	debugLogger, _ := mission.NewDebugLogger("")
	runner, err := mission.NewRunner(cfg, c.configPath, missionName, inputs, mission.WithThreadID(threadID), mission.WithDebugLogger(debugLogger), mission.WithHumanBridge(c), mission.WithGatewayBridge(c.gatewayBridge))
	if err != nil {
		c.concurrency.NotifyMissionDone(missionName)
		return "", "", fmt.Errorf("failed to create runner: %w", err)
	}

	// Create WS handler for tracking and event streaming
	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore(), runner.CostStore())

	// Run mission in background
	missionCtx, missionCancel := context.WithCancel(context.Background())
	go func() {
		c.runMissionChain(missionCtx, missionCancel, runner, storingHandler, wsHandler, missionName)
	}()

	// Wait for the mission ID
	missionID, err := wsHandler.WaitForMissionID(30 * time.Second)
	if err != nil {
		missionCancel()
		return "", "", fmt.Errorf("mission failed to start: %w", err)
	}

	// Track the running mission
	c.missionMu.Lock()
	c.runningMissions[missionID] = &runningMission{cancel: missionCancel, drain: runner.Drain}
	c.missionMu.Unlock()

	return missionID, runner.ThreadID(), nil
}

func (c *Client) handleGetCostSummary(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetCostSummaryPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_cost_summary: %w", err)
	}

	from, err := time.Parse(time.RFC3339, payload.From)
	if err != nil {
		from = time.Now().AddDate(0, -1, 0) // default: last 30 days
	}
	to, err := time.Parse(time.RFC3339, payload.To)
	if err != nil {
		to = time.Now()
	}

	groupBy := payload.GroupBy
	if groupBy == "" {
		groupBy = "date"
	}

	totals, err := c.stores.Costs.GetTotalCosts(from, to)
	if err != nil {
		return nil, fmt.Errorf("get total costs: %w", err)
	}

	byGroup, err := c.stores.Costs.GetCostSummary(from, to, groupBy)
	if err != nil {
		return nil, fmt.Errorf("get cost summary: %w", err)
	}

	recentMissions, err := c.stores.Costs.GetRecentMissionCosts(20)
	if err != nil {
		return nil, fmt.Errorf("get recent mission costs: %w", err)
	}

	// Convert store types to protocol types
	result := protocol.GetCostSummaryResultPayload{
		Totals: protocol.CostTotals{
			TotalCost:         totals.TotalCost,
			InputCost:         totals.InputCost,
			OutputCost:        totals.OutputCost,
			CacheReadCost:     totals.CacheReadCost,
			CacheWriteCost:    totals.CacheWriteCost,
			TotalTurns:        totals.TotalTurns,
			TotalInputTokens:  totals.TotalInputTokens,
			TotalOutputTokens: totals.TotalOutputTokens,
		},
	}

	for _, row := range byGroup {
		result.ByGroup = append(result.ByGroup, protocol.CostSummaryRow{
			GroupKey:       row.GroupKey,
			Turns:          row.Turns,
			TotalCost:      row.TotalCost,
			InputCost:      row.InputCost,
			OutputCost:     row.OutputCost,
			CacheReadCost:  row.CacheReadCost,
			CacheWriteCost: row.CacheWriteCost,
		})
	}

	for _, row := range recentMissions {
		result.RecentMissions = append(result.RecentMissions, protocol.MissionCostRow{
			MissionID:   row.MissionID,
			MissionName: row.MissionName,
			Status:      row.Status,
			Turns:       row.Turns,
			TotalCost:   row.TotalCost,
			StartedAt:   row.StartedAt.Format(time.RFC3339),
		})
	}

	// Optional: date × field breakdown for chart
	if payload.BreakdownField == "model" || payload.BreakdownField == "mission_name" {
		dateFieldRows, err := c.stores.Costs.GetCostsByDateAndField(from, to, payload.BreakdownField)
		if err == nil {
			for _, row := range dateFieldRows {
				result.ByDateAndField = append(result.ByDateAndField, protocol.DateFieldCostRow{
					Date:      row.Date,
					FieldKey:  row.FieldKey,
					TotalCost: row.TotalCost,
				})
			}
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetCostSummaryResult, &result)
}

func (c *Client) handleSubscribe(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.SubscribePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode subscribe: %w", err)
	}
	c.subscriptions.Subscribe(payload.Scope, payload.MissionID)
	return nil, nil
}

func (c *Client) handleUnsubscribe(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.UnsubscribePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode unsubscribe: %w", err)
	}
	c.subscriptions.Unsubscribe(payload.Scope, payload.MissionID)
	return nil, nil
}
