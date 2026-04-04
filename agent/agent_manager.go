package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/aitools"
	"squadron/config"
	"squadron/llm"
	"squadron/streamers"
)

// AgentManager owns agent creation, session tracking, and lifecycle for a commander.
// It centralizes the logic previously spread across callAgentTool.Call.
type AgentManager struct {
	mu sync.Mutex

	// Active agent sessions (multi-turn, may need resume)
	active map[string]*Agent

	// Completed agents (available for ask_agent follow-up queries)
	completed map[string]*completedAgent

	// Agent name → store session ID
	sessionIDs map[string]string

	// Dependencies from commander
	agents         map[string]*config.Agent
	configPath     string
	cfg            *config.Config
	secretInfos    []SecretInfo
	secretValues   map[string]string
	folderStore    aitools.FolderStore
	sessionLogger  SessionLogger
	taskID         string
	taskName       string
	iterationIndex   *int
	callbacks        *CommanderToolCallbacks
	debugLogger      DebugLogger
	pricingOverrides map[string]*llm.ModelPricing
	provider         llm.Provider // optional injected provider for agents
}

// AgentManagerConfig holds the dependencies needed to create an AgentManager.
type AgentManagerConfig struct {
	Agents         map[string]*config.Agent
	ConfigPath     string
	Config         *config.Config
	SecretInfos    []SecretInfo
	SecretValues   map[string]string
	FolderStore    aitools.FolderStore
	SessionLogger  SessionLogger
	TaskID         string
	TaskName       string
	IterationIndex *int
	Callbacks        *CommanderToolCallbacks
	DebugLogger      DebugLogger
	PricingOverrides map[string]*llm.ModelPricing
	// Provider is an optional pre-created LLM provider passed to spawned agents.
	Provider llm.Provider
}

// NewAgentManager creates a new AgentManager.
func NewAgentManager(cfg AgentManagerConfig) *AgentManager {
	return &AgentManager{
		active:         make(map[string]*Agent),
		completed:      make(map[string]*completedAgent),
		sessionIDs:     make(map[string]string),
		agents:         cfg.Agents,
		configPath:     cfg.ConfigPath,
		cfg:            cfg.Config,
		secretInfos:    cfg.SecretInfos,
		secretValues:   cfg.SecretValues,
		folderStore:    cfg.FolderStore,
		sessionLogger:  cfg.SessionLogger,
		taskID:         cfg.TaskID,
		taskName:       cfg.TaskName,
		iterationIndex: cfg.IterationIndex,
		callbacks:        cfg.Callbacks,
		debugLogger:      cfg.DebugLogger,
		pricingOverrides: cfg.PricingOverrides,
		provider:         cfg.Provider,
	}
}

// RunAgent runs an agent by name with a task or response. Blocks until the agent completes or errors.
// Returns the ChatResult and any error.
func (m *AgentManager) RunAgent(ctx context.Context, name, task, response string) (ChatResult, error) {
	agentCfg, ok := m.agents[name]
	if !ok {
		var available []string
		for n := range m.agents {
			available = append(available, n)
		}
		return ChatResult{}, fmt.Errorf("agent '%s' not found. Available agents: %v", name, available)
	}

	m.mu.Lock()
	a, exists := m.active[name]
	m.mu.Unlock()

	if exists {
		m.reopenSession(name)
	} else {
		var err error
		a, err = m.createAgent(ctx, agentCfg)
		if err != nil {
			return ChatResult{}, fmt.Errorf("creating agent '%s': %w", name, err)
		}
		m.mu.Lock()
		m.active[name] = a
		m.mu.Unlock()

		m.createSessionRecord(name, a)
	}

	m.setupDebugLogging(name, a)

	// Notify start (skip for resumed agents)
	instruction := task
	if response != "" {
		instruction = response
	}
	if !exists {
		m.notifyStart(name, instruction)
	}

	// Get handler
	var handler streamers.ChatHandler
	if m.callbacks != nil && m.callbacks.GetAgentHandler != nil {
		handler = m.callbacks.GetAgentHandler(m.taskName, name)
	}

	// Execute
	var result ChatResult
	var err error

	if exists && a.NeedsResume() {
		result, err = a.Resume(ctx, handler)
	} else {
		var agentInput string
		if response != "" {
			agentInput = fmt.Sprintf("<COMMANDER_RESPONSE>\n%s\n</COMMANDER_RESPONSE>", response)
			if handler != nil {
				handler.CommanderResponse(response)
			}
		} else {
			agentInput = fmt.Sprintf("<NEW_TASK>\n%s\n</NEW_TASK>", task)
		}
		result, err = a.Chat(ctx, agentInput, handler)
	}

	// Notify completion
	m.notifyComplete(name)

	if err != nil {
		m.completeSession(name, err)
		return ChatResult{}, err
	}

	// If agent completed, move to completed map
	if result.Complete {
		m.completeSession(name, nil)
		m.mu.Lock()
		m.completed[name] = &completedAgent{agent: a, agentID: name}
		m.mu.Unlock()
	}

	return result, nil
}

// GetCompleted returns a completed agent for follow-up queries (ask_agent).
func (m *AgentManager) GetCompleted(name string) (*Agent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ca, ok := m.completed[name]
	if !ok {
		return nil, false
	}
	return ca.agent, true
}

// AddRestoredCompleted adds a restored completed agent (for resume).
func (m *AgentManager) AddRestoredCompleted(name string, a *Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed[name] = &completedAgent{agent: a, agentID: name}
}

// AddRestoredActive adds a restored active agent (for resume).
func (m *AgentManager) AddRestoredActive(name string, a *Agent, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active[name] = a
	m.sessionIDs[name] = sessionID
	a.sessionLogger = m.sessionLogger
	a.sessionID = sessionID
	a.taskID = m.taskID
}

// GetSessionID returns the store session ID for an agent.
func (m *AgentManager) GetSessionID(name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sid, ok := m.sessionIDs[name]
	return sid, ok
}

// --- internal helpers ---

func (m *AgentManager) reopenSession(name string) {
	sid, ok := m.sessionIDs[name]
	if !ok {
		return
	}
	if m.sessionLogger != nil {
		m.sessionLogger.ReopenSession(sid)
	}
	if m.callbacks != nil && m.callbacks.OnSessionCreated != nil {
		m.callbacks.OnSessionCreated(m.taskName, name, sid)
	}
}

func (m *AgentManager) createAgent(ctx context.Context, agentCfg *config.Agent) (*Agent, error) {
	mode := config.ModeMission

	var datasetStore aitools.DatasetStore
	if m.callbacks != nil {
		datasetStore = m.callbacks.DatasetStore
	}

	var onCompaction func(int, int, int, int)
	if m.callbacks != nil && m.callbacks.OnAgentCompaction != nil {
		taskName := m.taskName
		agentName := agentCfg.Name
		cb := m.callbacks.OnAgentCompaction
		onCompaction = func(inputTokens, tokenLimit, messagesCompacted, turnRetention int) {
			cb(taskName, agentName, inputTokens, tokenLimit, messagesCompacted, turnRetention)
		}
	}

	var onSessionTurn func(protocol.SessionTurnData)
	if m.callbacks != nil && m.callbacks.OnAgentSessionTurn != nil {
		taskName := m.taskName
		agentName := agentCfg.Name
		cb := m.callbacks.OnAgentSessionTurn
		onSessionTurn = func(data protocol.SessionTurnData) {
			cb(taskName, agentName, data)
		}
	}

	return New(ctx, Options{
		Config:           m.cfg,
		ConfigPath:       m.configPath,
		AgentConfig:      agentCfg,
		AgentName:        agentCfg.Name,
		Mode:             &mode,
		DatasetStore:     datasetStore,
		SecretInfos:      m.secretInfos,
		SecretValues:     m.secretValues,
		FolderStore:      m.folderStore,
		OnCompaction:     onCompaction,
		OnSessionTurn:    onSessionTurn,
		PricingOverrides: m.pricingOverrides,
		Provider:         m.provider,
	})
}

func (m *AgentManager) createSessionRecord(name string, a *Agent) {
	if m.sessionLogger == nil {
		return
	}
	sid, err := m.sessionLogger.CreateSession(m.taskID, "agent", name, a.ModelName, m.iterationIndex)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.sessionIDs[name] = sid
	m.mu.Unlock()

	a.sessionLogger = m.sessionLogger
	a.sessionID = sid
	a.taskID = m.taskID

	if m.callbacks != nil && m.callbacks.OnSessionCreated != nil {
		m.callbacks.OnSessionCreated(m.taskName, name, sid)
	}

	// Persist agent system prompts
	now := time.Now()
	for _, sp := range a.session.GetSystemPrompts() {
		m.sessionLogger.AppendMessage(sid, "system", sp, now, now)
	}
}

func (m *AgentManager) setupDebugLogging(name string, a *Agent) {
	if a.eventLogger != nil || m.debugLogger == nil {
		return
	}
	debugName := fmt.Sprintf("%s_%s", m.taskName, name)
	a.EnableDebug(
		m.debugLogger.GetMessageFile("agent", debugName),
		m.debugLogger.GetTurnLogFile("agent", debugName),
		newContextEventLogger(m.debugLogger, map[string]any{
			"task":  m.taskName,
			"agent": name,
		}),
	)
}

func (m *AgentManager) notifyStart(name, instruction string) {
	if m.callbacks != nil && m.callbacks.OnAgentStart != nil {
		m.callbacks.OnAgentStart(m.taskName, name, instruction)
	}
}

func (m *AgentManager) notifyComplete(name string) {
	if m.callbacks != nil && m.callbacks.OnAgentComplete != nil {
		m.callbacks.OnAgentComplete(m.taskName, name)
	}
}

func (m *AgentManager) completeSession(name string, err error) {
	sid, ok := m.sessionIDs[name]
	if !ok || m.sessionLogger == nil {
		return
	}
	m.sessionLogger.CompleteSession(sid, err)
}
