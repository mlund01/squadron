package wsbridge

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mlund01/squadron-sdk/protocol"

	"squadron/agent"
	"squadron/config"
	"squadron/mission"
	"squadron/streamers"
)

func (c *Client) registerHandlers() {
	c.handlers[protocol.TypeGetConfig] = c.handleGetConfig
	c.handlers[protocol.TypeRunMission] = c.handleRunMission
	c.handlers[protocol.TypeGetMissions] = c.handleGetMissions
	c.handlers[protocol.TypeGetMission] = c.handleGetMission
	c.handlers[protocol.TypeGetTaskDetail] = c.handleGetTaskDetail
	c.handlers[protocol.TypeGetEvents] = c.handleGetEvents
	c.handlers[protocol.TypeChatMessage] = c.handleChatMessage
	c.handlers[protocol.TypeGetChatHistory] = c.handleGetChatHistory
	c.handlers[protocol.TypeGetChatMessages] = c.handleGetChatMessages
	c.handlers[protocol.TypeArchiveChat] = c.handleArchiveChat
}

func (c *Client) handleGetConfig(env *protocol.Envelope) (*protocol.Envelope, error) {
	instanceConfig := ConfigToInstanceConfig(c.cfg)
	return protocol.NewResponse(env.RequestID, protocol.TypeGetConfigResult, &protocol.GetConfigResultPayload{
		Config: instanceConfig,
	})
}

func (c *Client) handleRunMission(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.RunMissionPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode run_mission: %w", err)
	}

	// Validate mission exists
	var missionCfg *config.Mission
	for i := range c.cfg.Missions {
		if c.cfg.Missions[i].Name == payload.MissionName {
			missionCfg = &c.cfg.Missions[i]
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
	runner, err := mission.NewRunner(c.cfg, c.configPath, payload.MissionName, payload.Inputs, mission.WithDebugLogger(debugLogger))
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   err.Error(),
		})
	}

	// Create WS handler — it will capture the mission ID when MissionStarted fires
	wsHandler := NewWSMissionHandler(c)
	storingHandler := streamers.NewStoringMissionHandler(wsHandler, runner.EventStore())

	// Run mission in background
	go func() {
		if err := runner.Run(context.Background(), storingHandler); err != nil {
			log.Printf("Mission %q failed: %v", payload.MissionName, err)
			completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
				MissionID: wsHandler.MissionID(),
				Status:    "failed",
				Error:     err.Error(),
			})
			c.SendEvent(completeEnv)
		} else {
			completeEnv, _ := protocol.NewEvent(protocol.TypeMissionComplete, &protocol.MissionCompletePayload{
				MissionID: wsHandler.MissionID(),
				Status:    "completed",
			})
			c.SendEvent(completeEnv)
		}
	}()

	// Wait for the mission ID (set when Runner calls MissionStarted after creating the mission in the DB)
	missionID, err := wsHandler.WaitForMissionID(30 * time.Second)
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
			Accepted: false,
			Reason:   fmt.Sprintf("mission failed to start: %v", err),
		})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeRunMissionAck, &protocol.RunMissionAckPayload{
		Accepted:  true,
		MissionID: missionID,
	})
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
			StartedAt:  r.StartedAt.Format("2006-01-02T15:04:05Z"),
		}
		if r.FinishedAt != nil {
			s := r.FinishedAt.Format("2006-01-02T15:04:05Z")
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
		ID:        record.ID,
		Name:      record.MissionName,
		Status:    record.Status,
		StartedAt: record.StartedAt.Format("2006-01-02T15:04:05Z"),
	}
	if record.FinishedAt != nil {
		s := record.FinishedAt.Format("2006-01-02T15:04:05Z")
		missionInfo.FinishedAt = &s
	}

	tasks, err := c.stores.Missions.GetTasksByMission(payload.MissionID)
	if err != nil {
		return nil, fmt.Errorf("get tasks: %w", err)
	}

	taskInfos := make([]protocol.MissionTaskInfo, len(tasks))
	for i, t := range tasks {
		ti := protocol.MissionTaskInfo{
			ID:        t.ID,
			MissionID: t.MissionID,
			TaskName:  t.TaskName,
			Status:    t.Status,
			Summary:   t.Summary,
			OutputJSON: t.OutputJSON,
			Error:     t.Error,
		}
		if t.StartedAt != nil {
			s := t.StartedAt.Format("2006-01-02T15:04:05Z")
			ti.StartedAt = &s
		}
		if t.FinishedAt != nil {
			s := t.FinishedAt.Format("2006-01-02T15:04:05Z")
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

	// Get task outputs
	outputs, err := c.stores.Missions.GetTaskOutputs(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get task outputs: %w", err)
	}
	outputInfos := make([]protocol.TaskOutputInfo, len(outputs))
	for i, o := range outputs {
		outputInfos[i] = protocol.TaskOutputInfo{
			ID:          o.ID,
			TaskID:      o.TaskID,
			DatasetName: o.DatasetName,
			OutputJSON:  o.OutputJSON,
			Summary:     o.Summary,
			CreatedAt:   o.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	// Get sessions
	sessions, err := c.stores.Sessions.GetSessionsByTask(payload.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get sessions: %w", err)
	}
	sessionInfos := make([]protocol.SessionInfoDTO, len(sessions))
	for i, s := range sessions {
		sessionInfos[i] = protocol.SessionInfoDTO{
			ID:             s.ID,
			TaskID:         s.TaskID,
			Role:           s.Role,
			AgentName:      s.AgentName,
			Model:          s.Model,
			Status:         s.Status,
			StartedAt:      s.StartedAt.Format("2006-01-02T15:04:05Z"),
			IterationIndex: s.IterationIndex,
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetTaskDetailResult, &protocol.GetTaskDetailResultPayload{
		Outputs:  outputInfos,
		Sessions: sessionInfos,
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
		// Validate agent exists in config
		var agentCfg *config.Agent
		for i, ag := range c.cfg.Agents {
			if ag.Name == payload.AgentName {
				agentCfg = &c.cfg.Agents[i]
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
			Config:     c.cfg,
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
			c.stores.Sessions.AppendMessage(sessionID, "user", payload.Content)
		}

		_, chatErr := sess.agent.Chat(context.Background(), payload.Content, chatHandler)

		// Persist assistant answer
		if sessionID != "" {
			if answer := chatHandler.FullAnswer(); answer != "" {
				c.stores.Sessions.AppendMessage(sessionID, "assistant", answer)
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
			StartedAt: s.StartedAt.Format("2006-01-02T15:04:05Z"),
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

	messages := make([]protocol.ChatMessageInfo, len(msgs))
	for i, m := range msgs {
		messages[i] = protocol.ChatMessageInfo{
			ID:        m.ID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
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
			CreatedAt:      e.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeGetEventsResult, &protocol.GetEventsResultPayload{
		Events: eventInfos,
	})
}
