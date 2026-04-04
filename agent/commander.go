package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mlund01/squadron-wire/protocol"
	"github.com/zclconf/go-cty/cty"

	"squadron/agent/internal/prompts"
	"squadron/aitools"
	"squadron/config"
	"squadron/llm"
	"squadron/store"
	"squadron/streamers"
)

// DependencySummary holds the summary from a completed dependency task
type DependencySummary struct {
	TaskName string
	Summary  string
}

// SecretInfo contains name and description for a secret (passed to prompts)
type SecretInfo struct {
	Name        string
	Description string
}

// CommanderOptions holds configuration for creating a commander
type CommanderOptions struct {
	// Config is the loaded configuration
	Config *config.Config
	// ConfigPath is the path to the config directory (needed for spawning agents)
	ConfigPath string
	// MissionName is the name of the mission
	MissionName string
	// TaskName is the name of the task this commander is executing
	TaskName string
	// Commander is the model key for the commander (e.g., "claude_sonnet_4")
	Commander string
	// AgentNames is the list of agents available to this commander
	AgentNames []string
	// DepSummaries contains summaries from completed dependency tasks
	DepSummaries []DependencySummary
	// DepOutputSchemas contains output schema info for completed dependency tasks
	DepOutputSchemas []DependencyOutputSchema
	// TaskOutputSchema is the output schema for this task (if defined)
	TaskOutputSchema []OutputFieldSchema
	// PrevIterationOutput contains the structured output from the previous iteration (sequential only)
	PrevIterationOutput map[string]any
	// SecretInfos contains names and descriptions of available secrets (for prompts)
	SecretInfos []SecretInfo
	// SecretValues contains actual secret values for tool call injection
	SecretValues map[string]string
	// IsIteration indicates whether this commander is running an iteration of an iterated task
	IsIteration bool
	// IsParallel indicates whether the iteration is running in parallel (only relevant if IsIteration is true)
	IsParallel bool
	// DebugFile enables debug logging to the specified file (optional)
	DebugFile string
	// SequentialDataset contains all items for sequential iteration processing
	// When set, the commander handles all items in a single session using dataset_next/submit_output tools
	SequentialDataset []cty.Value
	// FolderStore provides file folder access for the mission (optional)
	FolderStore aitools.FolderStore
	// Compaction settings for the commander session (nil if disabled)
	Compaction *CompactionConfig
	// PruneOn triggers pruning when conversation reaches this many turns (0 = disabled)
	PruneOn int
	// PruneTo reduces conversation to this many turns when pruning triggers
	PruneTo int
	// Routes contains conditional routing options for this task (nil if no router)
	Routes []aitools.RouteOption
	// ToolResponseMaxSize overrides the default tool response size limit (0 = default)
	ToolResponseMaxSize int
	// Provider is an optional pre-created LLM provider. When set, commander creation
	// skips the internal provider factory and uses this provider instead.
	// The caller retains ownership — the commander will NOT close it.
	Provider llm.Provider
}

// DependencyOutputSchema describes a completed dependency task's output schema
type DependencyOutputSchema struct {
	TaskName     string
	IsIterated   bool
	ItemCount    int
	OutputFields []OutputFieldSchema
}

// OutputFieldSchema describes an output field
type OutputFieldSchema struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Items       *OutputFieldSchema  // For array/list types — describes the element type
	Properties  []OutputFieldSchema // For object types — describes inner fields
}

// CommanderToolCallbacks allows the mission to provide callbacks for commander tools
type CommanderToolCallbacks struct {
	// OnAgentStart is called when call_agent begins executing an agent
	OnAgentStart func(taskName, agentName, instruction string)
	// GetAgentHandler returns a ChatHandler for the agent execution
	GetAgentHandler func(taskName, agentName string) streamers.ChatHandler
	// OnAgentComplete is called when call_agent finishes executing an agent
	OnAgentComplete func(taskName, agentName string)
	// DatasetStore provides access to mission datasets for agent tools
	DatasetStore aitools.DatasetStore
	// KnowledgeStore provides access to completed task outputs for querying
	KnowledgeStore KnowledgeStore
	// DebugLogger provides debug logging capabilities (optional)
	DebugLogger DebugLogger
	// GetCommanderForQuery returns an isolated clone of a completed commander for querying.
	// The returned commander can answer questions without modifying the original's state.
	// The clone is cached per calling commander, so follow-up questions build on previous context.
	// For iterated tasks, pass the iteration index (0+). For regular tasks, pass -1.
	GetCommanderForQuery func(taskName string, iterationIndex int) (*Commander, error)

	// ListCommanderQuestions returns the list of questions already asked to a dependency task.
	// Used by iterations to see what questions have been asked by other iterations.
	ListCommanderQuestions func(taskName string) []string

	// GetCommanderAnswer returns the cached answer for a question by index.
	// Blocks until the answer is ready if another iteration is still querying.
	GetCommanderAnswer func(taskName string, index int) (string, error)

	// AskCommanderWithCache asks a question with deduplication. If the exact question was
	// already asked, returns the cached answer. Otherwise queries and caches.
	// For iterated tasks, pass the iteration index (0+). For regular tasks, pass -1.
	AskCommanderWithCache func(targetTask string, iterationIndex int, question string) (string, error)

	// OnSubmitOutput is called each time the LLM submits output via submit_output tool.
	// Used to persist task outputs incrementally.
	OnSubmitOutput aitools.SubmitOutputCallback

	// SessionLogger provides session persistence (optional). If set, commander and agent
	// sessions will be tracked with their message history.
	SessionLogger SessionLogger
	// TaskID is the store task ID for session creation (required if SessionLogger is set)
	TaskID string
	// IterationIndex identifies this specific iteration's session (nil for non-iterated tasks).
	IterationIndex *int
	// ExistingSessionID, if set, reuses this session instead of creating a new one.
	// Used when resuming from stored state — the runner finds the existing session
	// and passes its ID so messages continue appending to the same session.
	ExistingSessionID string

	// OnSessionCreated is called when a session is created (commander or agent).
	// Used to register session IDs with the event store for correlation.
	OnSessionCreated func(taskName, agentName, sessionID string)

	// OnAgentCompaction is called when an agent's context is compacted.
	// Receives taskName, agentName, and compaction metrics.
	OnAgentCompaction func(taskName, agentName string, inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int)

	// OnAgentSessionTurn is called after each agent LLM turn with telemetry data.
	OnAgentSessionTurn func(taskName, agentName string, data protocol.SessionTurnData)

	// Subtask management callbacks (optional). When set, the commander gets
	// set_subtasks, get_subtasks, and complete_subtask tools.
	SetSubtasks     func(titles []string) error
	GetSubtasks     func() ([]store.Subtask, error)
	CompleteSubtask func() error
}

// DebugLogger is the interface for debug logging during mission execution
type DebugLogger interface {
	// GetMessageFile returns a file path for logging LLM messages for a specific entity
	GetMessageFile(entityType, entityName string) string
	// GetTurnLogFile returns a .jsonl file path for per-turn session snapshots
	GetTurnLogFile(entityType, entityName string) string
	// LogEvent logs a programmatic event
	LogEvent(eventType string, data map[string]any)
}

// KnowledgeStore provides query access to completed task outputs
type KnowledgeStore interface {
	// GetTaskOutput returns a task's output by name
	GetTaskOutput(taskName string) (*TaskOutputInfo, bool)
	// Query returns iterations matching the query
	Query(taskName string, query TaskQuery) TaskQueryResult
	// Aggregate performs an aggregate operation on iterations
	Aggregate(taskName string, query AggregateQuery) AggregateResult
}

// TaskOutputInfo represents stored task output information
type TaskOutputInfo struct {
	TaskName        string
	Status          string
	IsIterated      bool
	TotalIterations int
	Iterations      []IterationInfo
	Output          map[string]any
}

// IterationInfo represents a single iteration's output
type IterationInfo struct {
	Index  int
	ItemID string
	Status string
	Output map[string]any
}

// TaskQuery represents a query for task outputs
type TaskQuery struct {
	Filters []TaskFilter
	Limit   int
	Offset  int
	OrderBy string
	Desc    bool
}

// TaskFilter represents a single filter condition
type TaskFilter struct {
	Field string
	Op    string // eq, ne, gt, lt, gte, lte, contains
	Value any
}

// TaskQueryResult represents the result of a query
type TaskQueryResult struct {
	TotalMatches int
	Results      []IterationInfo
}

// AggregateQuery represents an aggregate query
type AggregateQuery struct {
	Op      string // count, sum, avg, min, max, distinct, group_by
	Field   string
	Filters []TaskFilter
	GroupBy string
	GroupOp string
}

// AggregateResult represents the result of an aggregate query
type AggregateResult struct {
	Value  any
	Item   *IterationInfo
	Values []any
	Groups map[string]any
}

// SessionLogger provides session tracking for persistence.
// Implementations should be safe for concurrent use.
type SessionLogger interface {
	CreateSession(taskID, role, agentName, model string, iterationIndex *int) (id string, err error)
	CompleteSession(id string, err error)
	ReopenSession(id string)
	AppendMessage(sessionID, role, content string, createdAt, completedAt time.Time) error
	StoreToolResult(taskID, sessionID, toolCallId, toolName, inputParams, rawData string, startedAt, finishedAt time.Time) error
	StartToolCall(taskID, sessionID, toolCallId, toolName, inputParams string) (string, error)
	CompleteToolCall(id, rawData string) error
}

// CommanderStreamer is the interface for streaming commander events
type CommanderStreamer interface {
	ReasoningStarted()
	ReasoningCompleted(content string)
	Answer(content string)
	CallingTool(toolCallId, name, input string)
	ToolComplete(toolCallId, name string, result string)
	Compaction(inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int)
	SessionTurn(data protocol.SessionTurnData)
}

// completedAgent stores a completed agent instance for follow-up queries
type completedAgent struct {
	agent   *Agent
	agentID string
}

// Commander is an agent specialized for orchestrating other agents in a mission
type Commander struct {
	Name      string
	TaskName  string
	ModelName string

	session         *llm.Session
	tools           map[string]aitools.Tool
	provider        llm.Provider
	ownsProvider    bool
	agents          map[string]*config.Agent
	callbacks       *CommanderToolCallbacks
	configPath      string
	cfg             *config.Config
	resultStore     *aitools.MemoryResultStore
	interceptor     *aitools.ResultInterceptor
	completedAgents map[string]*completedAgent
	agentSessions   map[string]*Agent // Persistent agent sessions by name (for multi-turn interaction)
	debugLogger     DebugLogger
	turnLogger      *llm.TurnLogger
	queryClones     map[string]*Commander // Cached clones for ask_commander queries (keyed by target task name)
	secretInfos     []SecretInfo           // Secret names and descriptions for agent prompts
	secretValues    map[string]string      // Actual secret values for tool call injection
	datasetCursor      *aitools.DatasetCursor      // Cursor for sequential dataset iteration (nil if not sequential)
	submitOutput       *aitools.SubmitOutputTool   // Universal output submission tool
	taskComplete       *aitools.TaskCompleteTool   // Tool to signal task completion
	sessionLogger      SessionLogger               // Session persistence (nil if not tracking)
	sessionID          string                 // Store session ID (empty if not tracking)
	agentSessionIDs    map[string]string      // Agent name → store session ID (for agent session tracking)
	callbacksTaskID    string                 // Task ID from callbacks (for agent session creation)
	iterationIndex     *int                   // Iteration index (nil for non-iterated tasks)
	agentMgr           *AgentManager          // Manages agent lifecycle (creation, session, resume)
	subtasksSet        bool                   // Whether set_subtasks has been called
	folderStore        aitools.FolderStore    // Folder access for missions (nil if not configured)
	compaction         *CompactionConfig      // Compaction settings (nil if disabled)
	pruneOn            int                    // Trigger pruning at this many turns (0 = disabled)
	pruneTo            int                    // Prune down to this many turns
}

// NewCommander creates a new commander for a mission task
func NewCommander(ctx context.Context, opts CommanderOptions) (*Commander, error) {
	// Resolve the commander model
	modelConfig, actualModelName, err := resolveCommander(opts.Config, opts.Commander)
	if err != nil {
		return nil, fmt.Errorf("resolving commander model: %w", err)
	}

	// Use injected provider or create one from config
	var provider llm.Provider
	var ownsProvider bool
	if opts.Provider != nil {
		provider = opts.Provider
		ownsProvider = false
	} else {
		if modelConfig.APIKey == "" {
			return nil, fmt.Errorf("API key not set for model '%s'", modelConfig.Name)
		}
		provider, ownsProvider, err = createCommanderProvider(ctx, modelConfig)
		if err != nil {
			return nil, fmt.Errorf("creating provider: %w", err)
		}
	}

	// Get agent configs and build agent info for the prompt
	agents := make(map[string]*config.Agent)
	var agentInfos []prompts.AgentInfo
	for _, agentName := range opts.AgentNames {
		for i := range opts.Config.Agents {
			if opts.Config.Agents[i].Name == agentName {
				agents[agentName] = &opts.Config.Agents[i]
				agentInfos = append(agentInfos, prompts.AgentInfo{
					Name:        agentName,
					Description: opts.Config.Agents[i].Role,
				})
				break
			}
		}
	}

	// Build system prompts
	var systemPrompts []string

	// Main commander prompt with iteration context
	iterationOpts := prompts.IterationOptions{
		IsIteration: opts.IsIteration,
		IsParallel:  opts.IsParallel,
	}
	systemPrompts = append(systemPrompts, prompts.GetCommanderPrompt(agentInfos, iterationOpts))

	// Add context about mission and task
	systemPrompts = append(systemPrompts, fmt.Sprintf(
		"You are executing task '%s' in mission '%s'.",
		opts.TaskName, opts.MissionName,
	))

	// Create session
	session := llm.NewSession(provider, actualModelName, systemPrompts...)
	// Conversation caching is disabled when pruning is active since the shifting
	// message window invalidates the cache every turn, wasting cache creation tokens.
	conversationCaching := modelConfig.IsPromptCachingEnabled() && (opts.PruneOn == 0 || (opts.PruneOn-opts.PruneTo) >= 3)
	session.SetPromptCaching(modelConfig.IsPromptCachingEnabled(), conversationCaching)

	// Set stop sequences to prevent LLM from hallucinating observations
	session.SetStopSequences([]string{"___STOP___"})

	if opts.DebugFile != "" {
		if err := session.EnableDebug(opts.DebugFile); err != nil {
			fmt.Printf("Warning: could not enable debug logging: %v\n", err)
		}
	}

	// Create result store and interceptor for large results
	resultStore := aitools.NewMemoryResultStore()
	resultConfig := aitools.LargeResultConfigWithMaxSize(opts.ToolResponseMaxSize)
	interceptor := aitools.NewResultInterceptor(resultStore, resultConfig)

	sup := &Commander{
		Name:            fmt.Sprintf("%s/%s", opts.MissionName, opts.TaskName),
		TaskName:        opts.TaskName,
		ModelName:       actualModelName,
		session:         session,
		tools:           make(map[string]aitools.Tool),
		provider:        provider,
		ownsProvider:    ownsProvider,
		agents:          agents,
		configPath:      opts.ConfigPath,
		cfg:             opts.Config,
		resultStore:     resultStore,
		interceptor:     interceptor,
		completedAgents: make(map[string]*completedAgent),
		agentSessions:   make(map[string]*Agent),
		secretInfos:     opts.SecretInfos,
		secretValues:    opts.SecretValues,
		compaction:      opts.Compaction,
		pruneOn:         opts.PruneOn,
		pruneTo:         opts.PruneTo,
	}

	// Add result tools to commander's tool map
	sup.tools["result_info"] = &aitools.ResultInfoTool{Store: resultStore}
	sup.tools["result_items"] = &aitools.ResultItemsTool{Store: resultStore}
	sup.tools["result_get"] = &aitools.ResultGetTool{Store: resultStore}
	sup.tools["result_keys"] = &aitools.ResultKeysTool{Store: resultStore}
	sup.tools["result_chunk"] = &aitools.ResultChunkTool{Store: resultStore}

	// Add folder tools if FolderStore is available
	if opts.FolderStore != nil {
		sup.folderStore = opts.FolderStore
		sup.tools["file_list"] = &aitools.FolderListTool{Store: opts.FolderStore}
		sup.tools["file_read"] = &aitools.FolderReadTool{Store: opts.FolderStore}
		sup.tools["file_create"] = &aitools.FolderCreateTool{Store: opts.FolderStore}
		sup.tools["file_delete"] = &aitools.FolderDeleteTool{Store: opts.FolderStore}
		sup.tools["file_search"] = &aitools.FolderSearchTool{Store: opts.FolderStore}
		sup.tools["file_grep"] = &aitools.FolderGrepTool{Store: opts.FolderStore}
		if folderPrompt := prompts.FormatFolderContext(opts.FolderStore); folderPrompt != "" {
			session.AddSystemPrompt(folderPrompt)
		}
	}

	// If there are dependency summaries or output schemas, add them as a secondary system prompt
	if len(opts.DepSummaries) > 0 || len(opts.DepOutputSchemas) > 0 {
		sup.injectDependencyContext(opts.DepSummaries, opts.DepOutputSchemas)
	}

	// Create submit_output tool only if task has an explicit output schema
	if len(opts.TaskOutputSchema) > 0 {
		var outputFields []aitools.OutputField
		for _, f := range opts.TaskOutputSchema {
			outputFields = append(outputFields, aitools.OutputField{
				Name:     f.Name,
				Type:     f.Type,
				Required: f.Required,
			})
		}
		sup.submitOutput = aitools.NewSubmitOutputTool(outputFields)
		sup.tools["submit_output"] = sup.submitOutput
		sup.injectOutputSchemaInstructions(opts.TaskOutputSchema)
	}

	// Register task_complete tool (always available)
	sup.taskComplete = &aitools.TaskCompleteTool{
		Routes: opts.Routes,
	}
	sup.tools["task_complete"] = sup.taskComplete

	// If there's previous iteration output (sequential iterations), inject it
	if len(opts.PrevIterationOutput) > 0 {
		sup.injectPrevIterationOutput(opts.PrevIterationOutput)
	}

	// If sequential dataset is provided, set up cursor and dataset_next tool
	if len(opts.SequentialDataset) > 0 {
		sup.datasetCursor = aitools.NewDatasetCursor(opts.TaskName, opts.SequentialDataset)
		nextTool := aitools.NewDatasetNextTool(sup.datasetCursor)
		if sup.submitOutput != nil {
			nextTool.OutputCounter = sup.submitOutput.ResultCount
		}
		sup.tools["dataset_next"] = nextTool
		sup.injectSequentialDatasetInstructions(len(opts.SequentialDataset))
	}

	return sup, nil
}

// SetToolCallbacks configures the callbacks for commander tools
// This must be called before ExecuteTask to enable call_agent and ask_agent
func (s *Commander) SetToolCallbacks(callbacks *CommanderToolCallbacks, depSummaries []DependencySummary) {
	s.callbacks = callbacks
	s.debugLogger = callbacks.DebugLogger
	s.iterationIndex = callbacks.IterationIndex

	// Create or reuse session record if session logger is available
	if callbacks.SessionLogger != nil {
		s.sessionLogger = callbacks.SessionLogger
		s.callbacksTaskID = callbacks.TaskID
		s.agentSessionIDs = make(map[string]string)
		if callbacks.ExistingSessionID != "" {
			// Reuse existing session (resume — found by runner from stored state)
			s.sessionID = callbacks.ExistingSessionID
			if callbacks.OnSessionCreated != nil {
				callbacks.OnSessionCreated(s.TaskName, "commander", s.sessionID)
			}
		} else {
			// Create new session
			if id, err := callbacks.SessionLogger.CreateSession(callbacks.TaskID, "commander", "", s.ModelName, callbacks.IterationIndex); err != nil {
				log.Printf("Commander %s: failed to create session: %v", s.TaskName, err)
			} else {
				s.sessionID = id
				if callbacks.OnSessionCreated != nil {
					callbacks.OnSessionCreated(s.TaskName, "commander", id)
				}
				// Persist system prompts to store
				now := time.Now()
				for _, sp := range s.session.GetSystemPrompts() {
					s.sessionLogger.AppendMessage(id, "system", sp, now, now)
				}
			}
		}
	}

	// Create turn logger for commander session snapshots
	if s.debugLogger != nil {
		turnLogFile := s.debugLogger.GetTurnLogFile("commander", s.TaskName)
		if turnLogFile != "" {
			if tl, err := llm.NewTurnLogger(turnLogFile); err == nil {
				s.turnLogger = tl
			}
		}
	}

	// Build call_agent tool
	s.tools["call_agent"] = &callAgentTool{
		commander: s,
	}

	// Build ask_agent tool for querying completed agents
	s.tools["ask_agent"] = &askAgentTool{
		commander: s,
	}

	// Add dataset tools if DatasetStore is available
	if callbacks.DatasetStore != nil {
		s.tools["set_dataset"] = &aitools.SetDatasetTool{Store: callbacks.DatasetStore}
		s.tools["dataset_sample"] = &aitools.DatasetSampleTool{Store: callbacks.DatasetStore}
		s.tools["dataset_count"] = &aitools.DatasetCountTool{Store: callbacks.DatasetStore}
		s.tools["result_to_dataset"] = &aitools.ResultToDatasetTool{
			ResultStore:  s.resultStore,
			DatasetStore: callbacks.DatasetStore,
		}
	}

	// Add query_task_output tool if KnowledgeStore is available
	if callbacks.KnowledgeStore != nil {
		s.tools["query_task_output"] = &queryTaskOutputTool{
			store: callbacks.KnowledgeStore,
		}
	}

	// Wire OnSubmitOutput callback to submit_output tool if set
	if callbacks.OnSubmitOutput != nil && s.submitOutput != nil {
		s.submitOutput.OnSubmit = callbacks.OnSubmitOutput
	}

	// Add ask_commander tool if GetCommanderForQuery callback is available
	if callbacks.GetCommanderForQuery != nil {
		s.tools["ask_commander"] = &askCommanderTool{
			commander: s,
		}
	}

	// Add iteration-specific tools if callbacks are available
	if callbacks.ListCommanderQuestions != nil {
		s.tools["list_commander_questions"] = &listCommanderQuestionsTool{
			commander: s,
		}
	}
	if callbacks.GetCommanderAnswer != nil {
		s.tools["get_commander_answer"] = &getCommanderAnswerTool{
			commander: s,
		}
	}

	// Wire up subtask checker on task_complete
	if callbacks.SetSubtasks != nil && callbacks.GetSubtasks != nil {
		s.taskComplete.SubtaskChecker = func() (int, int) {
			subtasks, err := callbacks.GetSubtasks()
			if err != nil || len(subtasks) == 0 {
				return 0, 0
			}
			incomplete := 0
			for _, st := range subtasks {
				if st.Status != "completed" {
					incomplete++
				}
			}
			return len(subtasks), incomplete
		}
	}

	// Register subtask tools if callbacks are provided
	if callbacks.SetSubtasks != nil {
		setTool := &setSubtasksTool{
			onSet: func(titles []string) error {
				s.subtasksSet = true
				return callbacks.SetSubtasks(titles)
			},
			onGet: callbacks.GetSubtasks,
		}
		s.tools["set_subtasks"] = setTool
		s.tools["get_subtasks"] = &getSubtasksTool{onGet: callbacks.GetSubtasks}
		s.tools["complete_subtask"] = &completeSubtaskTool{
			onComplete: callbacks.CompleteSubtask,
			onGet:      callbacks.GetSubtasks,
		}

		// Wire up dataset_next <-> subtask integration for sequential tasks
		if nextTool, ok := s.tools["dataset_next"].(*aitools.DatasetNextTool); ok {
			// dataset_next checks subtasks are complete before advancing
			nextTool.SubtaskChecker = func() (int, int) {
				subtasks, err := callbacks.GetSubtasks()
				if err != nil || len(subtasks) == 0 {
					return 0, 0
				}
				incomplete := 0
				for _, st := range subtasks {
					if st.Status != "completed" {
						incomplete++
					}
				}
				return len(subtasks), incomplete
			}
			// When dataset_next advances, allow set_subtasks to redefine
			origOnNext := s.datasetCursor.OnNext
			s.datasetCursor.OnNext = func(index int) {
				setTool.datasetAdvanced = true
				if origOnNext != nil {
					origOnNext(index)
				}
			}
		}

		// Restore subtasksSet flag if resuming with existing subtasks
		if callbacks.ExistingSessionID != "" && callbacks.GetSubtasks != nil {
			if subtasks, err := callbacks.GetSubtasks(); err == nil && len(subtasks) > 0 {
				s.subtasksSet = true
			}
		}
	}

	// Inject commander tools as a system prompt so the LLM knows about them
	if toolsPrompt := prompts.FormatCommanderTools(s.tools); toolsPrompt != "" {
		s.session.AddSystemPrompt(toolsPrompt)
	}

	// Initialize AgentManager
	s.agentMgr = NewAgentManager(AgentManagerConfig{
		Agents:         s.agents,
		ConfigPath:     s.configPath,
		Config:         s.cfg,
		SecretInfos:    s.secretInfos,
		SecretValues:   s.secretValues,
		FolderStore:    s.folderStore,
		SessionLogger:  s.sessionLogger,
		TaskID:         s.callbacksTaskID,
		TaskName:       s.TaskName,
		IterationIndex: s.iterationIndex,
		Callbacks:      callbacks,
		DebugLogger:    s.debugLogger,
		Provider:       s.provider,
	})
}

// injectDependencyContext adds a secondary system prompt with dependency summaries and output schemas
func (s *Commander) injectDependencyContext(summaries []DependencySummary, outputSchemas []DependencyOutputSchema) {
	var sb strings.Builder
	sb.WriteString("## Completed Dependency Tasks\n\n")

	// Add summaries
	if len(summaries) > 0 {
		sb.WriteString("The following tasks have been completed. Use their summaries for context.\n\n")
		for _, summary := range summaries {
			sb.WriteString(fmt.Sprintf("### Task: %s\n", summary.TaskName))
			sb.WriteString(fmt.Sprintf("%s\n\n", summary.Summary))
		}
	}

	// Add output schemas with query instructions
	if len(outputSchemas) > 0 {
		sb.WriteString("## Queryable Task Outputs\n\n")
		sb.WriteString("Use `query_task_output` to access structured data from these completed tasks:\n\n")

		for _, schema := range outputSchemas {
			if schema.IsIterated {
				sb.WriteString(fmt.Sprintf("### Task: %s (iterated, %d items)\n", schema.TaskName, schema.ItemCount))
			} else {
				sb.WriteString(fmt.Sprintf("### Task: %s\n", schema.TaskName))
			}

			if len(schema.OutputFields) > 0 {
				sb.WriteString("**Output fields:**\n")
				for _, field := range schema.OutputFields {
					req := ""
					if field.Required {
						req = " (required)"
					}
					desc := ""
					if field.Description != "" {
						desc = " - " + field.Description
					}
					sb.WriteString(fmt.Sprintf("- `%s` (%s%s)%s\n", field.Name, field.Type, req, desc))
				}
			}

			sb.WriteString("\n**Example queries:**\n")
			sb.WriteString(fmt.Sprintf("- Get all: `{\"task\": \"%s\"}`\n", schema.TaskName))
			if schema.IsIterated && len(schema.OutputFields) > 0 {
				field := schema.OutputFields[0].Name
				sb.WriteString(fmt.Sprintf("- Filter: `{\"task\": \"%s\", \"filters\": [{\"field\": \"%s\", \"op\": \"gt\", \"value\": 0}]}`\n", schema.TaskName, field))
				sb.WriteString(fmt.Sprintf("- Aggregate: `{\"task\": \"%s\", \"aggregate\": {\"op\": \"avg\", \"field\": \"%s\"}}`\n", schema.TaskName, field))
			}
			sb.WriteString("\n")
		}
	}

	s.session.AddSystemPrompt(sb.String())
}

// formatFieldType returns a human-readable type string including nested type info
func formatFieldType(field OutputFieldSchema) string {
	switch field.Type {
	case "array", "list":
		if field.Items != nil {
			return "list<" + formatFieldType(*field.Items) + ">"
		}
		return "list<any>"
	case "map":
		if field.Items != nil {
			return "map<string, " + formatFieldType(*field.Items) + ">"
		}
		return "map<string, any>"
	case "object":
		if len(field.Properties) > 0 {
			return "object"
		}
		return "object"
	default:
		return field.Type
	}
}

// writeFieldList writes output field descriptions to sb at the given indent level
func writeFieldList(sb *strings.Builder, fields []OutputFieldSchema, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, field := range fields {
		req := ""
		if field.Required {
			req = " (required)"
		}
		desc := ""
		if field.Description != "" {
			desc = " - " + field.Description
		}
		typeName := formatFieldType(field)
		sb.WriteString(fmt.Sprintf("%s- `%s` (%s%s)%s\n", prefix, field.Name, typeName, req, desc))

		// Render nested object properties
		if field.Type == "object" && len(field.Properties) > 0 {
			writeFieldList(sb, field.Properties, indent+1)
		}
	}
}

// writeExampleJSON writes a JSON example value for a field
func writeExampleJSON(field OutputFieldSchema) string {
	switch field.Type {
	case "number":
		return "0.0"
	case "integer":
		return "0"
	case "boolean":
		return "false"
	case "string":
		return "\"...\""
	case "array", "list":
		if field.Items != nil {
			return "[" + writeExampleJSON(*field.Items) + "]"
		}
		return "[]"
	case "map":
		if field.Items != nil {
			return "{\"key\": " + writeExampleJSON(*field.Items) + "}"
		}
		return "{}"
	case "object":
		if len(field.Properties) > 0 {
			parts := make([]string, 0, len(field.Properties))
			for _, prop := range field.Properties {
				parts = append(parts, fmt.Sprintf("\"%s\": %s", prop.Name, writeExampleJSON(prop)))
			}
			return "{" + strings.Join(parts, ", ") + "}"
		}
		return "{}"
	default:
		return "\"...\""
	}
}

// injectOutputSchemaInstructions adds instructions for producing structured output via submit_output tool
func (s *Commander) injectOutputSchemaInstructions(schema []OutputFieldSchema) {
	var sb strings.Builder
	sb.WriteString("## Required Structured Output\n\n")
	sb.WriteString("This task requires structured output. You MUST call the `submit_output` tool to deliver your results.\n\n")

	sb.WriteString("**Output fields:**\n")
	writeFieldList(&sb, schema, 0)

	sb.WriteString("\n**Example submit_output call:**\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\"output\": {")

	for i, field := range schema {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("\"%s\": %s", field.Name, writeExampleJSON(field)))
	}

	sb.WriteString("}, \"summary\": \"Brief description of what was done\"}\n")
	sb.WriteString("```\n\n")
	sb.WriteString("Call `submit_output` with the output object containing the fields above, then provide your final ANSWER.\n")

	s.session.AddSystemPrompt(sb.String())
}

// injectPrevIterationOutput adds context about the previous iteration's output (for sequential iterations)
func (s *Commander) injectPrevIterationOutput(prevOutput map[string]any) {
	prevJSON, err := json.MarshalIndent(prevOutput, "", "  ")
	if err != nil {
		// Fallback to simple format if marshaling fails
		prevJSON = []byte(fmt.Sprintf("%v", prevOutput))
	}

	prompt := fmt.Sprintf(`## Previous Iteration Output

You are processing one item in a sequential iteration. The PREVIOUS item (a different item from the dataset) produced this output:
%s

This is NOT the same item you are processing now - it's from the previous dataset item.
Use this context only if relevant (e.g., pagination cursors, cumulative state, or patterns to follow).
Your current task is for a NEW item with its own parameters.
`, string(prevJSON))

	s.session.AddSystemPrompt(prompt)
}


// GetSubmitResults returns all outputs submitted via the submit_output tool
func (s *Commander) GetSubmitResults() []aitools.SubmitResult {
	if s.submitOutput == nil {
		return nil
	}
	return s.submitOutput.GetResults()
}

// GetStoredResults returns all tool results stored during this commander's session
func (s *Commander) GetStoredResults() []*aitools.StoredResult {
	if s.resultStore == nil {
		return nil
	}
	return s.resultStore.GetAll()
}

// GetSessionID returns the commander's session ID (for associating tool results)
func (s *Commander) GetSessionID() string {
	return s.sessionID
}

// IsTaskSucceeded returns whether task_complete was called with succeed=true (or default).
func (s *Commander) IsTaskSucceeded() bool {
	return s.taskComplete.IsSucceeded()
}

// TaskFailureReason returns the reason provided when task_complete was called with succeed=false.
func (s *Commander) TaskFailureReason() string {
	return s.taskComplete.FailureReason()
}

// ChosenRoute returns the route chosen by the commander, or "" if none.
func (s *Commander) ChosenRoute() string {
	return s.taskComplete.ChosenRoute()
}

// IsMissionRoute returns true if the chosen route targets a mission.
func (s *Commander) IsMissionRoute() bool {
	return s.taskComplete.IsMissionRoute()
}

// MissionInputs returns the inputs provided for a mission route, or nil.
func (s *Commander) MissionInputs() map[string]string {
	return s.taskComplete.MissionInputs()
}

// HasSequentialDataset returns true if this commander is processing a sequential dataset
func (s *Commander) HasSequentialDataset() bool {
	return s.datasetCursor != nil
}

// SetDatasetOnNext sets a callback that fires when the dataset cursor advances to a new item.
func (s *Commander) SetDatasetOnNext(fn func(index int)) {
	if s.datasetCursor != nil {
		s.datasetCursor.OnNext = fn
	}
}

// GetCurrentDatasetIndex returns the current dataset cursor index (0-based),
// or nil if there's no sequential dataset cursor.
func (s *Commander) GetCurrentDatasetIndex() *int {
	if s.datasetCursor == nil {
		return nil
	}
	idx := s.datasetCursor.CurrentIndex()
	if idx < 0 {
		return nil
	}
	return &idx
}

// injectSequentialDatasetInstructions adds instructions for processing a sequential dataset
func (s *Commander) injectSequentialDatasetInstructions(itemCount int) {
	prompt := fmt.Sprintf(`## Sequential Dataset Processing

You have a dataset of %d items to process sequentially in this single session.

**IMPORTANT: For sequential dataset tasks, the normal "set_subtasks as first action" rule is replaced by the workflow below. Follow these steps exactly.**

**Tools:**
- dataset_next: Get the next item. Returns {"status": "ok", "index": N, "total": M, "item": {...}} or {"status": "exhausted"}
- submit_output: Submit output for current item. Required before calling dataset_next again.
- set_subtasks: **MANDATORY** — define subtasks for each item after calling dataset_next.
- complete_subtask: Mark the current subtask as done. You MUST call this for each subtask.

**Workflow (follow this exactly for EVERY item):**
1. Call dataset_next to get an item
2. Call set_subtasks to define subtasks for this item — this is REQUIRED, not optional
3. Work through each subtask, calling complete_subtask after finishing each one
4. Call submit_output with the structured output for this item
5. Repeat from step 1 until dataset_next returns "exhausted"
6. Call task_complete when all items are processed

You MUST call submit_output before dataset_next or you will get an error.
You MUST use set_subtasks and complete_subtask for every item — do not skip them.
`, itemCount)

	s.session.AddSystemPrompt(prompt)
}

// ExecuteTask runs the task objective to completion
func (s *Commander) ExecuteTask(ctx context.Context, objective string, streamer CommanderStreamer) error {
	return s.runLoop(ctx, objective, false, streamer)
}

// ExecuteOrResume checks if the session has stored messages (loaded via LoadSessionMessages).
// If so, it resumes from where the prior run left off. Otherwise, it starts fresh.
func (s *Commander) ExecuteOrResume(ctx context.Context, objective string, streamer CommanderStreamer) error {
	if len(s.session.GetHistory()) > 0 {
		return s.ResumeTask(ctx, streamer)
	}
	return s.ExecuteTask(ctx, objective, streamer)
}

// ResumeTask resumes an interrupted task from stored session messages.
// It analyzes the last stored message to determine where to pick up:
// - Last message is user: LLM was interrupted mid-response — use ContinueStream
// - Last message is assistant with call_agent ACTION: re-execute (agent resumes from its own session)
// - Last message is assistant with other ACTION: inject placeholder observation
// - Last message is assistant with no action: task was already complete
func (s *Commander) ResumeTask(ctx context.Context, streamer CommanderStreamer) error {
	msgs := s.session.GetHistory()
	if len(msgs) == 0 {
		return fmt.Errorf("no messages to resume from")
	}
	last := msgs[len(msgs)-1]

	if last.Role == llm.RoleUser {
		// LLM was interrupted mid-response.
		// Session already has the pending user message — use ContinueStream.
		return s.runLoop(ctx, "", true, streamer)
	}

	// Last message is assistant
	parser := newCommanderParser(streamer)
	parser.ProcessChunk(last.Content)
	parser.Finish()

	action := parser.GetAction()
	if action == "" {
		// No action — consider done
		if s.turnLogger != nil {
			s.turnLogger.Close()
		}
		if s.sessionLogger != nil && s.sessionID != "" {
			s.sessionLogger.CompleteSession(s.sessionID, nil)
		}
		return nil
	}

	// Tool call was in-flight — handle based on tool type
	actionInput := parser.GetActionInput()
	var currentInput string

	if action == "call_agent" {
		// EXCEPTION: re-execute call_agent — agent resumes from its own stored session
		tool := s.tools[action]
		if tool == nil {
			currentInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found.\n</OBSERVATION>", action)
		} else {
			tcID := uuid.New().String()
			streamer.CallingTool(tcID, action, actionInput)

			var toolRecordID string
			if s.sessionLogger != nil && s.sessionID != "" {
				toolRecordID, _ = s.sessionLogger.StartToolCall(s.callbacksTaskID, s.sessionID, tcID, action, actionInput)
			}

			result := tool.Call(ctx, actionInput)

			if toolRecordID != "" {
				s.sessionLogger.CompleteToolCall(toolRecordID, result)
			}

			var observationContent string
			currentInput, observationContent = s.formatObservation(action, result)
			streamer.ToolComplete(tcID, action, observationContent)
		}
	} else {
		// DEFAULT: result was lost, tell the LLM
		currentInput = fmt.Sprintf(
			"<OBSERVATION>\nThe result of this %s call was lost due to an interruption. "+
				"You may need to run it again or attempt to verify whether the call was successful.\n</OBSERVATION>",
			action,
		)
	}

	// Continue the loop with the observation (normal send, not resume)
	return s.runLoop(ctx, currentInput, false, streamer)
}

// LoadSessionMessages loads persisted messages into the commander's session.
// Used when resaturating a commander from stored state (e.g., mission resume).
func (s *Commander) LoadSessionMessages(msgs []llm.Message) {
	s.session.LoadMessages(msgs)
}

// AddRestoredAgent adds a restored agent to the commander's completed agents map.
// Used during mission resume to reconstruct agent state from stored sessions.
func (s *Commander) AddRestoredAgent(agentName string, a *Agent) {
	if s.agentMgr != nil {
		s.agentMgr.AddRestoredCompleted(agentName, a)
	} else {
		// Fallback for commanders without AgentManager (e.g. resaturated commanders)
		if s.completedAgents == nil {
			s.completedAgents = make(map[string]*completedAgent)
		}
		s.completedAgents[agentName] = &completedAgent{
			agent:   a,
			agentID: agentName,
		}
	}
}

// AddRestoredActiveAgent adds a restored agent to the commander's active agent sessions.
// Unlike AddRestoredAgent (which adds to completedAgents for ask_agent queries), this
// adds to agentSessions so that call_agent finds and reuses the agent instead of creating
// a fresh one. Used when resuming interrupted tasks where the agent was mid-execution.
func (s *Commander) AddRestoredActiveAgent(agentName string, a *Agent, sessionID string) {
	if s.agentMgr != nil {
		s.agentMgr.AddRestoredActive(agentName, a, sessionID)
	} else {
		s.agentSessions[agentName] = a
		if s.agentSessionIDs == nil {
			s.agentSessionIDs = make(map[string]string)
		}
		s.agentSessionIDs[agentName] = sessionID
		a.sessionLogger = s.sessionLogger
		a.sessionID = sessionID
	}
	a.taskID = s.callbacksTaskID
	// Wire up telemetry callback so resumed agents emit session_turn events
	if s.callbacks != nil && s.callbacks.OnAgentSessionTurn != nil {
		taskName := s.TaskName
		cb := s.callbacks.OnAgentSessionTurn
		a.onSessionTurn = func(data protocol.SessionTurnData) {
			cb(taskName, agentName, data)
		}
	}
}

// runLoop is the core execution loop shared by ExecuteTask and ResumeTask.
// When resume=true, the first LLM call uses ContinueStream (no new user message,
// no store logging) because the session already has a pending user message.
func (s *Commander) runLoop(ctx context.Context, currentInput string, resume bool, streamer CommanderStreamer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Create parser for this message
		parser := newCommanderParser(streamer)

		if s.debugLogger != nil {
			s.debugLogger.LogEvent("commander_llm_start", map[string]any{"task": s.TaskName})
		}
		llmStart := time.Now()

		var resp *llm.ChatResponse
		var err error

		if resume {
			// First turn of resume — LLM responds to existing state.
			// Don't log to session store (the pending message is already stored).
			resp, err = s.session.ContinueStream(ctx, func(chunk llm.StreamChunk) {
				if chunk.Content != "" {
					parser.ProcessChunk(chunk.Content)
				}
			})
			resume = false
		} else {
			// Normal turn — log user message, then send
			if s.sessionLogger != nil && s.sessionID != "" {
				now := time.Now()
				s.sessionLogger.AppendMessage(s.sessionID, "user", currentInput, now, now)
			}
			resp, err = s.session.SendStream(ctx, currentInput, func(chunk llm.StreamChunk) {
				if chunk.Content != "" {
					parser.ProcessChunk(chunk.Content)
				}
			})
		}

		if s.debugLogger != nil {
			eventData := map[string]any{
				"task":        s.TaskName,
				"duration_ms": time.Since(llmStart).Milliseconds(),
			}
			if resp != nil {
				eventData["input_tokens"] = resp.Usage.InputTokens
				eventData["output_tokens"] = resp.Usage.OutputTokens
				if resp.Usage.CacheWriteTokens > 0 {
					eventData["cache_write_tokens"] = resp.Usage.CacheWriteTokens
				}
				if resp.Usage.CacheReadTokens > 0 {
					eventData["cache_read_tokens"] = resp.Usage.CacheReadTokens
				}
			}
			s.debugLogger.LogEvent("commander_llm_end", eventData)
		}

		// Check if compaction is needed after response
		if resp != nil {
			s.checkAndCompact(resp.Usage.InputTokens, streamer)
		}

		// Apply turn limit pruning after compaction
		s.applyTurnPruning(streamer)

		// Emit session turn telemetry
		if resp != nil {
			stats := s.session.MessageStats()
			streamer.SessionTurn(protocol.SessionTurnData{
				Model:                     s.ModelName,
				InputTokens:              resp.Usage.InputTokens,
				OutputTokens:             resp.Usage.OutputTokens,
				CacheWriteTokens: resp.Usage.CacheWriteTokens,
				CacheReadTokens:  resp.Usage.CacheReadTokens,
				UserMessages:             stats.UserCount,
				AssistantMessages:        stats.AssistantCount,
				SystemMessages:           stats.SystemCount,
				PayloadBytes:             stats.PayloadBytes,
				TurnDurationMs:           time.Since(llmStart).Milliseconds(),
			})
		}

		parser.Finish()

		if err != nil {
			return err
		}

		// Log assistant response to session store
		if s.sessionLogger != nil && s.sessionID != "" && resp != nil {
			s.sessionLogger.AppendMessage(s.sessionID, "assistant", resp.Content, llmStart, time.Now())
		}

		// Determine action for turn logging
		action := parser.GetAction()

		// Log turn snapshot
		if s.turnLogger != nil {
			s.turnLogger.LogTurn(action, s.session.SnapshotMessages())
		}

		if action == "" {
			break // No tool call, done with this turn
		}

		actionInput := parser.GetActionInput()
		tcID := uuid.New().String()
		streamer.CallingTool(tcID, action, actionInput)

		// Log tool call event
		if s.debugLogger != nil {
			s.debugLogger.LogEvent("tool_call", map[string]any{
				"task":   s.TaskName,
				"tool":   action,
				"input":  actionInput,
			})
		}

		// Look up the tool
		tool := s.tools[action]
		if tool == nil {
			errMsg := fmt.Sprintf("Error: Tool '%s' not found. Available tools: call_agent, ask_agent", action)
			streamer.ToolComplete(tcID, action, errMsg)
			currentInput = fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", errMsg)
			continue
		}

		// Write-ahead: record tool call before execution
		var toolRecordID string
		if s.sessionLogger != nil && s.sessionID != "" {
			toolRecordID, _ = s.sessionLogger.StartToolCall(s.callbacksTaskID, s.sessionID, tcID, action, actionInput)
		}

		// Execute the tool
		toolStart := time.Now()
		result := tool.Call(ctx, actionInput)

		// Complete the tool call record
		if toolRecordID != "" {
			s.sessionLogger.CompleteToolCall(toolRecordID, result)
		}

		// Format observation (may intercept/truncate large results)
		var observationContent string
		currentInput, observationContent = s.formatObservation(action, result)
		streamer.ToolComplete(tcID, action, observationContent)

		// Log tool result event
		if s.debugLogger != nil {
			s.debugLogger.LogEvent("tool_result", map[string]any{
				"task":        s.TaskName,
				"tool":        action,
				"result":      result,
				"duration_ms": time.Since(toolStart).Milliseconds(),
			})
		}

		// Check if task_complete was called
		if s.taskComplete.IsCompleted() {
			break
		}
	}

	if s.turnLogger != nil {
		s.turnLogger.Close()
	}

	// Complete the session record
	if s.sessionLogger != nil && s.sessionID != "" {
		s.sessionLogger.CompleteSession(s.sessionID, nil)
	}

	return nil
}

// checkAndCompact checks if compaction is needed and performs it if so
func (s *Commander) checkAndCompact(inputTokens int, streamer CommanderStreamer) {
	if s.compaction == nil || s.compaction.TokenLimit <= 0 {
		return
	}

	if inputTokens <= s.compaction.TokenLimit {
		return
	}

	extraContext := s.buildCompactionContext()
	compacted := s.session.CompactWithContext(s.compaction.TurnRetention, extraContext)
	if compacted > 0 {
		streamer.Compaction(inputTokens, s.compaction.TokenLimit, compacted, s.compaction.TurnRetention)
		if s.debugLogger != nil {
			s.debugLogger.LogEvent("commander_compaction", map[string]any{
				"input_tokens":       inputTokens,
				"token_limit":        s.compaction.TokenLimit,
				"messages_compacted": compacted,
				"turn_retention":     s.compaction.TurnRetention,
			})
		}
	}
}

// buildCompactionContext returns commander-specific state for the compaction summary
func (s *Commander) buildCompactionContext() string {
	var sb strings.Builder
	sb.WriteString("**Commander State:**\n")

	// Dataset progress (sequential iteration)
	if s.datasetCursor != nil {
		current := s.datasetCursor.CurrentIndex()
		total := s.datasetCursor.Total()
		sb.WriteString(fmt.Sprintf("- Dataset progress: item %d of %d\n", current+1, total))
	}

	// Subtask status
	if s.callbacks != nil && s.callbacks.GetSubtasks != nil {
		subtasks, err := s.callbacks.GetSubtasks()
		if err == nil && len(subtasks) > 0 {
			completed := 0
			for _, st := range subtasks {
				if st.Status == "completed" {
					completed++
				}
			}
			sb.WriteString(fmt.Sprintf("- Subtasks: %d/%d complete\n", completed, len(subtasks)))
			for _, st := range subtasks {
				sb.WriteString(fmt.Sprintf("  - %s: %s\n", st.Title, st.Status))
			}
		}
	}

	// Output count
	if s.submitOutput != nil {
		count := s.submitOutput.ResultCount()
		if count > 0 {
			sb.WriteString(fmt.Sprintf("- Outputs submitted: %d\n", count))
		}
	}

	return sb.String()
}

// applyTurnPruning drops old messages when the conversation reaches the prune_on threshold,
// reducing it down to prune_to turns.
func (s *Commander) applyTurnPruning(streamer CommanderStreamer) {
	if s.pruneOn <= 0 {
		return
	}
	pruneOnMessages := s.pruneOn * 2
	if s.session.MessageCount() < pruneOnMessages {
		return
	}

	pruneToMessages := s.pruneTo * 2
	dropped := s.session.DropOldMessages(pruneToMessages)
	if dropped > 0 {
		streamer.Compaction(0, 0, dropped, s.pruneTo)
		if s.debugLogger != nil {
			s.debugLogger.LogEvent("commander_pruning", map[string]any{
				"messages_dropped": dropped,
				"prune_on":        s.pruneOn,
				"prune_to":        s.pruneTo,
			})
		}
	}
}

// AnswerQuestion handles a follow-up question from another commander (via ask_commander)
func (s *Commander) AnswerQuestion(ctx context.Context, question string) (string, error) {
	prompt := fmt.Sprintf(`<QUESTION>
Another commander is asking for clarification about your completed task.
Question: %s

Please provide a concise, factual answer based on what you learned during your task execution.
</QUESTION>`, question)

	resp, err := s.session.Send(ctx, prompt)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// Close releases resources held by the commander
func (s *Commander) Close() {
	// Close any cached query clones (from ask_commander)
	for _, clone := range s.queryClones {
		if clone != nil {
			clone.Close()
		}
	}
	s.queryClones = nil

	// Close persistent agent sessions (for multi-turn interaction)
	for _, a := range s.agentSessions {
		if a != nil {
			a.Close()
		}
	}
	s.agentSessions = nil

	for _, ca := range s.completedAgents {
		if ca.agent != nil {
			ca.agent.Close()
		}
	}
	s.completedAgents = nil

	if s.session != nil {
		s.session.Close()
	}
	if s.ownsProvider {
		if closer, ok := s.provider.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

// CloneForQuery creates an isolated copy of this commander for answering a question.
// The clone has a copy of the session state (conversation history) but operates independently.
// Multiple clones can be created and queried in parallel without affecting each other.
// The clone shares the same provider and completed agents (for ask_agent support).
func (s *Commander) CloneForQuery() *Commander {
	// Clone the session for isolated query processing
	clonedSession := s.session.Clone()

	// Copy completed agents map (shallow copy - agents are shared for ask_agent)
	completedAgentsCopy := make(map[string]*completedAgent)
	for id, ca := range s.completedAgents {
		completedAgentsCopy[id] = &completedAgent{
			agent:   ca.agent,
			agentID: ca.agentID,
		}
	}

	// Create result store and interceptor for the clone
	resultStore := aitools.NewMemoryResultStore()
	interceptor := aitools.NewResultInterceptor(resultStore, aitools.DefaultLargeResultConfig())

	clone := &Commander{
		Name:            s.Name + "_clone",
		TaskName:        s.TaskName,
		ModelName:       s.ModelName,
		session:         clonedSession,
		tools:           make(map[string]aitools.Tool),
		provider:        s.provider,     // Shared - providers are thread-safe
		ownsProvider:    false,          // Clone doesn't own the provider
		agents:          s.agents,       // Shared - config is read-only
		callbacks:       s.callbacks,    // Shared - callbacks are stateless
		configPath:      s.configPath,
		cfg:             s.cfg,
		resultStore:     resultStore,
		interceptor:     interceptor,
		completedAgents: completedAgentsCopy,
		debugLogger:     nil, // No debug logging for query clones
	}

	// Add result tools
	clone.tools["result_info"] = &aitools.ResultInfoTool{Store: resultStore}
	clone.tools["result_items"] = &aitools.ResultItemsTool{Store: resultStore}
	clone.tools["result_get"] = &aitools.ResultGetTool{Store: resultStore}
	clone.tools["result_keys"] = &aitools.ResultKeysTool{Store: resultStore}
	clone.tools["result_chunk"] = &aitools.ResultChunkTool{Store: resultStore}

	// Add ask_agent tool so the clone can query its agents
	clone.tools["ask_agent"] = &askAgentTool{commander: clone}

	return clone
}

// AnswerQueryIsolated answers a follow-up question using an isolated session.
// This is called on a cloned commander and does not affect the original.
// It runs an execution loop to handle any tool calls (like ask_agent) before returning.
func (s *Commander) AnswerQueryIsolated(ctx context.Context, question string) (string, error) {
	currentInput := fmt.Sprintf(`<QUESTION>
Another commander is asking for additional information about your completed task.
Question: %s

You may use ask_agent to query your agents if you need more details from them.
Provide a concise, factual answer based on what you learned during your task execution.
Wrap your final answer in <ANSWER> tags.
</QUESTION>`, question)

	// Run execution loop to handle any tool calls
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		resp, err := s.session.Send(ctx, currentInput)
		if err != nil {
			return "", err
		}

		content := resp.Content

		// Check if there's an action to call
		action, actionInput := s.parseActionFromContent(content)
		if action == "" {
			// No tool call, extract and return the answer
			return s.extractAnswer(content), nil
		}

		// Look up and execute the tool
		tool := s.tools[action]
		if tool == nil {
			currentInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found. Available tools: ask_agent\n</OBSERVATION>", action)
			continue
		}

		result := tool.Call(ctx, actionInput)
		currentInput, _ = s.formatObservation(action, result)
	}
}

// parseActionFromContent extracts ACTION and ACTION_INPUT from a response
func (s *Commander) parseActionFromContent(content string) (action, actionInput string) {
	// Find <ACTION>...</ACTION>
	actionStart := strings.Index(content, "<ACTION>")
	if actionStart == -1 {
		return "", ""
	}
	actionEnd := strings.Index(content[actionStart:], "</ACTION>")
	if actionEnd == -1 {
		return "", ""
	}
	action = strings.TrimSpace(content[actionStart+8 : actionStart+actionEnd])

	// Find <ACTION_INPUT>...</ACTION_INPUT>
	inputStart := strings.Index(content, "<ACTION_INPUT>")
	if inputStart == -1 {
		return action, ""
	}
	inputEnd := strings.Index(content[inputStart:], "</ACTION_INPUT>")
	if inputEnd == -1 {
		return action, ""
	}
	actionInput = strings.TrimSpace(content[inputStart+14 : inputStart+inputEnd])

	return action, actionInput
}

// extractAnswer extracts the answer content from a response
func (s *Commander) extractAnswer(content string) string {
	if idx := strings.Index(content, "<ANSWER>"); idx != -1 {
		content = content[idx+8:]
		if endIdx := strings.Index(content, "</ANSWER>"); endIdx != -1 {
			content = content[:endIdx]
		}
	}
	return strings.TrimSpace(content)
}

// formatObservation formats a tool result as an observation, with optional metadata.
// Returns the formatted observation string and the observation content (what the LLM sees inside the tags).
func (s *Commander) formatObservation(toolName, result string) (formatted string, content string) {
	if s.interceptor == nil {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", result), result
	}

	ir := s.interceptor.Intercept(toolName, result)
	if ir.Metadata == "" {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", ir.Data), ir.Data
	}

	return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>\n<OBSERVATION_METADATA>\n%s\n</OBSERVATION_METADATA>", ir.Data, ir.Metadata), ir.Data
}

// ExecuteAggregation performs a simple LLM call for summary aggregation (no tools)
func (s *Commander) ExecuteAggregation(ctx context.Context, prompt string) (string, error) {
	resp, err := s.session.Send(ctx, prompt)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// resolveCommander finds the model config for a model key
func resolveCommander(cfg *config.Config, modelKey string) (*config.Model, string, error) {
	for i := range cfg.Models {
		m := &cfg.Models[i]
		supportedModels, ok := config.SupportedModels[m.Provider]
		if !ok {
			continue
		}

		for _, allowedKey := range m.AllowedModels {
			if allowedKey == modelKey {
				actualModel, ok := supportedModels[modelKey]
				if !ok {
					return nil, "", fmt.Errorf("model key '%s' not found in supported models for provider '%s'", modelKey, m.Provider)
				}
				return m, actualModel, nil
			}
		}
	}

	return nil, "", fmt.Errorf("no model config found for model '%s'", modelKey)
}

// createCommanderProvider creates the appropriate LLM provider based on config
func createCommanderProvider(ctx context.Context, modelConfig *config.Model) (llm.Provider, bool, error) {
	switch modelConfig.Provider {
	case config.ProviderOpenAI:
		return llm.NewOpenAIProvider(modelConfig.APIKey), false, nil
	case config.ProviderAnthropic:
		return llm.NewAnthropicProvider(modelConfig.APIKey), false, nil
	case config.ProviderGemini:
		provider, err := llm.NewGeminiProvider(ctx, modelConfig.APIKey)
		if err != nil {
			return nil, false, err
		}
		return provider, true, nil
	default:
		return nil, false, fmt.Errorf("unknown provider: %s", modelConfig.Provider)
	}
}

// =============================================================================
// Commander Parser - parses ReAct-formatted streaming output
// =============================================================================

// commanderParserState represents the current parsing state
type commanderParserState int

const (
	commanderStateNone commanderParserState = iota
	commanderStateReasoning
	commanderStateAction
	commanderStateActionInput
)

// commanderParser parses ReAct-formatted streaming output for commanders
type commanderParser struct {
	streamer         CommanderStreamer
	state            commanderParserState
	buffer           strings.Builder
	reasoningStarted bool
	actionName       string
	actionInput      string
	reasoningText    strings.Builder
}

// newCommanderParser creates a new parser with the given streamer
func newCommanderParser(streamer CommanderStreamer) *commanderParser {
	return &commanderParser{
		streamer: streamer,
		state:    commanderStateNone,
	}
}

// ProcessChunk processes an incoming chunk of streamed content
func (p *commanderParser) ProcessChunk(chunk string) {
	p.buffer.WriteString(chunk)
	p.processBuffer()
}

// GetAction returns the parsed action name
func (p *commanderParser) GetAction() string {
	return p.actionName
}

// GetActionInput returns the parsed action input
func (p *commanderParser) GetActionInput() string {
	return p.actionInput
}

// Finish signals that streaming is complete
func (p *commanderParser) Finish() {
	// Nothing special needed
}

func (p *commanderParser) processBuffer() {
	content := p.buffer.String()

	for {
		switch p.state {
		case commanderStateNone:
			// Look for opening tags
			if idx := strings.Index(content, "<REASONING>"); idx != -1 {
				p.streamer.ReasoningStarted()
				p.state = commanderStateReasoning
				content = content[idx+11:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ACTION>"); idx != -1 {
				p.state = commanderStateAction
				content = content[idx+8:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ACTION_INPUT>"); idx != -1 {
				p.state = commanderStateActionInput
				content = content[idx+14:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return

		case commanderStateReasoning:
			if !p.reasoningStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.reasoningStarted = true
				}
			}

			if idx := strings.Index(content, "</REASONING>"); idx != -1 {
				finalContent := strings.TrimRight(content[:idx], "\n")
				if len(finalContent) > 0 {
					p.reasoningText.WriteString(finalContent)
				}
				if p.reasoningText.Len() > 0 {
					p.streamer.ReasoningCompleted(p.reasoningText.String())
				}
				p.reasoningText.Reset()
				p.reasoningStarted = false
				p.state = commanderStateNone
				content = content[idx+12:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			// Buffer content for reasoning
			if len(content) > 12 {
				safeLen := len(content) - 12
				p.reasoningText.WriteString(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return

		case commanderStateAction:
			if idx := strings.Index(content, "</ACTION>"); idx != -1 {
				p.actionName = strings.TrimSpace(content[:idx])
				p.state = commanderStateNone
				content = content[idx+9:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return

		case commanderStateActionInput:
			if idx := strings.Index(content, "</ACTION_INPUT>"); idx != -1 {
				p.actionInput = strings.TrimSpace(content[:idx])
				p.state = commanderStateNone
				content = content[idx+15:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return
		}
	}
}

// =============================================================================
// Commander Tools - call_agent and ask_agent
// =============================================================================

// callAgentTool is the tool for delegating work to agents
type callAgentTool struct {
	commander *Commander
}

func (t *callAgentTool) ToolName() string {
	return "call_agent"
}

func (t *callAgentTool) ToolDescription() string {
	return `Call an agent to perform a task or respond to an agent's question.

Use "task" to assign a new task (always starts fresh, ignores any in-flight work).
Use "response" to answer an agent's ASK_COMMANDER question (agent continues where it left off).

Provide exactly one of "task" or "response", not both.`
}

func (t *callAgentTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"name": {
				Type:        aitools.TypeString,
				Description: "The name of the agent to call",
			},
			"task": {
				Type:        aitools.TypeString,
				Description: "A new task for the agent. Always treated as a fresh assignment.",
			},
			"response": {
				Type:        aitools.TypeString,
				Description: "Response to an agent's ASK_COMMANDER question. Agent continues from where it left off.",
			},
		},
		Required: []string{"name"},
	}
}

func (t *callAgentTool) Call(ctx context.Context, input string) string {
	var params struct {
		Name     string `json:"name"`
		Task     string `json:"task"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: Invalid input: %v", err)
	}

	// Validate: must have either task or response, not both
	if params.Task == "" && params.Response == "" {
		return "Error: Must provide either 'task' or 'response'"
	}
	if params.Task != "" && params.Response != "" {
		return "Error: Cannot provide both 'task' and 'response'"
	}

	result, err := t.commander.agentMgr.RunAgent(ctx, params.Name, params.Task, params.Response)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if result.AskCommander != "" {
		return result.AskCommander
	}

	if result.Complete {
		return result.Answer
	}

	return "Agent did not produce a result. Call again to continue."
}

// askAgentTool is the tool for querying completed agents
type askAgentTool struct {
	commander *Commander
}

func (t *askAgentTool) ToolName() string {
	return "ask_agent"
}

func (t *askAgentTool) ToolDescription() string {
	return "Ask a follow-up question to a previously completed agent. Use this when you need more details than what was provided in the agent's initial answer. The agent will answer from its existing context without executing new tool calls."
}

func (t *askAgentTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"agent_id": {
				Type:        aitools.TypeString,
				Description: "The agent_id returned from a previous call_agent response",
			},
			"question": {
				Type:        aitools.TypeString,
				Description: "The follow-up question to ask the agent",
			},
		},
		Required: []string{"agent_id", "question"},
	}
}

func (t *askAgentTool) Call(ctx context.Context, input string) string {
	var params struct {
		AgentID  string `json:"agent_id"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: Invalid input: %v", err)
	}

	// Look up the completed agent via manager (or legacy map for cloned/resaturated commanders)
	var agent *Agent
	if t.commander.agentMgr != nil {
		agent, _ = t.commander.agentMgr.GetCompleted(params.AgentID)
	}
	if agent == nil {
		ca := t.commander.completedAgents[params.AgentID]
		if ca == nil {
			return fmt.Sprintf("Error: Agent '%s' not found or not yet completed", params.AgentID)
		}
		agent = ca.agent
	}

	answer, err := agent.AnswerFollowUp(ctx, params.Question)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return answer
}

// =============================================================================
// askCommanderTool - queries completed commanders from dependency tasks
// =============================================================================

// askCommanderTool is the tool for querying completed commanders in the dependency lineage
type askCommanderTool struct {
	commander *Commander
}

func (t *askCommanderTool) ToolName() string {
	return "ask_commander"
}

func (t *askCommanderTool) ToolDescription() string {
	return `Ask a follow-up question to a completed commander from a dependency task. Use this when you need more details than what was provided in the task summary.

The queried commander will answer from its existing context and can use ask_agent to query its own agents if needed.

**For iterated tasks:** Use the "index" parameter to query a specific iteration's commander. Get the index from query_task_output results (each iteration has an "index" field).

**Context behavior:** The first query to a commander creates a clone from its completed state. Subsequent queries to the same commander build on previous questions and answers, enabling natural follow-up conversations.`
}

func (t *askCommanderTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task_name": {
				Type:        aitools.TypeString,
				Description: "The name of the completed dependency task to query",
			},
			"question": {
				Type:        aitools.TypeString,
				Description: "The follow-up question to ask the commander",
			},
			"index": {
				Type:        aitools.TypeInteger,
				Description: "For iterated tasks: the iteration index to query (from query_task_output). Omit for regular tasks.",
			},
		},
		Required: []string{"task_name", "question"},
	}
}

func (t *askCommanderTool) Call(ctx context.Context, input string) string {
	var params struct {
		TaskName string `json:"task_name"`
		Question string `json:"question"`
		Index    *int   `json:"index"` // nil for regular tasks, 0+ for iterated tasks
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: Invalid input: %v", err)
	}

	// Determine iteration index (-1 for regular tasks)
	iterIndex := -1
	if params.Index != nil {
		iterIndex = *params.Index
	}

	// Use cached query if available (for iteration deduplication)
	if t.commander.callbacks != nil && t.commander.callbacks.AskCommanderWithCache != nil {
		answer, err := t.commander.callbacks.AskCommanderWithCache(params.TaskName, iterIndex, params.Question)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return answer
	}

	// Fallback to per-commander clone logic (for non-iteration contexts)
	if t.commander.callbacks == nil || t.commander.callbacks.GetCommanderForQuery == nil {
		return "Error: ask_commander is not available in this context"
	}

	if t.commander.queryClones == nil {
		t.commander.queryClones = make(map[string]*Commander)
	}

	cacheKey := params.TaskName
	if iterIndex >= 0 {
		cacheKey = fmt.Sprintf("%s[%d]", params.TaskName, iterIndex)
	}

	supClone, exists := t.commander.queryClones[cacheKey]
	if !exists {
		var err error
		supClone, err = t.commander.callbacks.GetCommanderForQuery(params.TaskName, iterIndex)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		t.commander.queryClones[cacheKey] = supClone
	}

	answer, err := supClone.AnswerQueryIsolated(ctx, params.Question)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return answer
}

// =============================================================================
// listCommanderQuestionsTool - lists questions asked to dependency commanders
// =============================================================================

// listCommanderQuestionsTool shows what questions have been asked to a dependency task by other iterations
type listCommanderQuestionsTool struct {
	commander *Commander
}

func (t *listCommanderQuestionsTool) ToolName() string {
	return "list_commander_questions"
}

func (t *listCommanderQuestionsTool) ToolDescription() string {
	return `List questions that have been asked to a dependency commander by other iterations.

Use this to see what information has already been requested, so you can reuse existing answers instead of asking duplicate questions. Use get_commander_answer to retrieve the answer for a specific question by its index.`
}

func (t *listCommanderQuestionsTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task_name": {
				Type:        aitools.TypeString,
				Description: "The name of the dependency task to list questions for",
			},
		},
		Required: []string{"task_name"},
	}
}

func (t *listCommanderQuestionsTool) Call(ctx context.Context, input string) string {
	var params struct {
		TaskName string `json:"task_name"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: Invalid input: %v", err)
	}

	if t.commander.callbacks == nil || t.commander.callbacks.ListCommanderQuestions == nil {
		return "Error: list_commander_questions is not available in this context"
	}

	questions := t.commander.callbacks.ListCommanderQuestions(params.TaskName)
	if len(questions) == 0 {
		return "No questions have been asked yet."
	}

	var sb strings.Builder
	for i, q := range questions {
		sb.WriteString(fmt.Sprintf("%d: %q\n", i, q))
	}
	return sb.String()
}

// =============================================================================
// getCommanderAnswerTool - gets cached answer by index
// =============================================================================

// getCommanderAnswerTool retrieves a cached answer for a question by its index
type getCommanderAnswerTool struct {
	commander *Commander
}

func (t *getCommanderAnswerTool) ToolName() string {
	return "get_commander_answer"
}

func (t *getCommanderAnswerTool) ToolDescription() string {
	return `Get the answer for a previously asked question by its index.

Use list_commander_questions first to see available questions and their indices. If the answer is still being fetched by another iteration, this will wait until it's ready.`
}

func (t *getCommanderAnswerTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task_name": {
				Type:        aitools.TypeString,
				Description: "The name of the dependency task",
			},
			"index": {
				Type:        aitools.TypeInteger,
				Description: "The index of the question (from list_commander_questions)",
			},
		},
		Required: []string{"task_name", "index"},
	}
}

func (t *getCommanderAnswerTool) Call(ctx context.Context, input string) string {
	var params struct {
		TaskName string `json:"task_name"`
		Index    int    `json:"index"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: Invalid input: %v", err)
	}

	if t.commander.callbacks == nil || t.commander.callbacks.GetCommanderAnswer == nil {
		return "Error: get_commander_answer is not available in this context"
	}

	answer, err := t.commander.callbacks.GetCommanderAnswer(params.TaskName, params.Index)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return answer
}

// =============================================================================
// queryTaskOutputTool - queries completed task outputs
// =============================================================================

// queryTaskOutputTool allows commanders to query completed dependency task outputs
type queryTaskOutputTool struct {
	store KnowledgeStore
}

func (t *queryTaskOutputTool) ToolName() string {
	return "query_task_output"
}

func (t *queryTaskOutputTool) ToolDescription() string {
	return `Query structured outputs from completed dependency tasks. Returns only the structured data fields defined in the task's output schema.

**Note:** For narrative summaries or detailed explanations, use ask_commander instead.

**Query modes:**
1. Get structured output: {"task": "task_name"}
2. Filter iterations: {"task": "task_name", "filters": [{"field": "temperature", "op": "lt", "value": 32}]}
3. Get specific items: {"task": "task_name", "item_ids": ["Chicago_IL", "Detroit_MI"]}
4. Aggregate: {"task": "task_name", "aggregate": {"op": "avg", "field": "temperature"}}
5. Group by: {"task": "task_name", "aggregate": {"op": "group_by", "group_by": "state", "group_op": "avg", "field": "temperature"}}

**Filter operators:** eq, ne, gt, lt, gte, lte, contains`
}

func (t *queryTaskOutputTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task": {
				Type:        aitools.TypeString,
				Description: "The name of the completed task to query",
			},
			"filters": {
				Type:        aitools.TypeArray,
				Description: "Filter conditions: [{field, op, value}]. Ops: eq, ne, gt, lt, gte, lte, contains",
			},
			"item_ids": {
				Type:        aitools.TypeArray,
				Description: "Specific item IDs to retrieve (for iterated tasks)",
			},
			"limit": {
				Type:        aitools.TypeInteger,
				Description: "Maximum number of results to return (default: 20)",
			},
			"offset": {
				Type:        aitools.TypeInteger,
				Description: "Number of results to skip",
			},
			"order_by": {
				Type:        aitools.TypeString,
				Description: "Field to sort by",
			},
			"desc": {
				Type:        aitools.TypeBoolean,
				Description: "Sort in descending order",
			},
			"aggregate": {
				Type:        aitools.TypeObject,
				Description: "Aggregate operation: {op, field, group_by, group_op}. Ops: count, sum, avg, min, max, distinct, group_by",
			},
		},
		Required: []string{"task"},
	}
}

func (t *queryTaskOutputTool) Call(ctx context.Context, input string) string {
	var params struct {
		Task      string `json:"task"`
		Filters   []struct {
			Field string `json:"field"`
			Op    string `json:"op"`
			Value any    `json:"value"`
		} `json:"filters"`
		ItemIDs   []string `json:"item_ids"`
		Limit     int      `json:"limit"`
		Offset    int      `json:"offset"`
		OrderBy   string   `json:"order_by"`
		Desc      bool     `json:"desc"`
		Aggregate *struct {
			Op      string `json:"op"`
			Field   string `json:"field"`
			GroupBy string `json:"group_by"`
			GroupOp string `json:"group_op"`
		} `json:"aggregate"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	// Get the task output
	output, ok := t.store.GetTaskOutput(params.Task)
	if !ok {
		return fmt.Sprintf("Error: task '%s' not found or not yet completed", params.Task)
	}

	// If aggregate query, handle it
	if params.Aggregate != nil {
		filters := make([]TaskFilter, len(params.Filters))
		for i, f := range params.Filters {
			filters[i] = TaskFilter{Field: f.Field, Op: f.Op, Value: f.Value}
		}

		result := t.store.Aggregate(params.Task, AggregateQuery{
			Op:      params.Aggregate.Op,
			Field:   params.Aggregate.Field,
			Filters: filters,
			GroupBy: params.Aggregate.GroupBy,
			GroupOp: params.Aggregate.GroupOp,
		})

		return formatAggregateResult(result)
	}

	// For non-iterated tasks, just return the summary and output
	if !output.IsIterated {
		return formatTaskOutput(output)
	}

	// For iterated tasks, handle query/filter
	if len(params.ItemIDs) > 0 {
		// Return specific items by ID
		var results []IterationInfo
		for _, iter := range output.Iterations {
			for _, id := range params.ItemIDs {
				if iter.ItemID == id {
					results = append(results, iter)
					break
				}
			}
		}
		return formatIterationResults(params.Task, results, len(results))
	}

	// Build and execute query
	filters := make([]TaskFilter, len(params.Filters))
	for i, f := range params.Filters {
		filters[i] = TaskFilter{Field: f.Field, Op: f.Op, Value: f.Value}
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	result := t.store.Query(params.Task, TaskQuery{
		Filters: filters,
		Limit:   limit,
		Offset:  params.Offset,
		OrderBy: params.OrderBy,
		Desc:    params.Desc,
	})

	return formatIterationResults(params.Task, result.Results, result.TotalMatches)
}

// formatTaskOutput formats a non-iterated task output
func formatTaskOutput(output *TaskOutputInfo) string {
	// Only return structured output - summaries are accessed via ask_commander
	if len(output.Output) > 0 {
		outputJSON, _ := json.MarshalIndent(output.Output, "", "  ")
		return fmt.Sprintf("Task: %s\nStatus: %s\n\nOutput:\n%s", output.TaskName, output.Status, string(outputJSON))
	}
	return fmt.Sprintf("Task: %s\nStatus: %s\n\nOutput: (none)", output.TaskName, output.Status)
}

// formatIterationResults formats iteration query results (structured output only)
func formatIterationResults(taskName string, results []IterationInfo, totalMatches int) string {
	if len(results) == 0 {
		return fmt.Sprintf("Task '%s': No matching iterations found", taskName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task '%s': %d matches (showing %d)\n\n", taskName, totalMatches, len(results)))

	for _, iter := range results {
		sb.WriteString(fmt.Sprintf("--- %s (index %d) ---\n", iter.ItemID, iter.Index))
		if len(iter.Output) > 0 {
			outputJSON, _ := json.MarshalIndent(iter.Output, "", "  ")
			sb.WriteString(fmt.Sprintf("Output: %s\n", string(outputJSON)))
		} else {
			sb.WriteString("Output: (none)\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatAggregateResult formats an aggregate query result
func formatAggregateResult(result AggregateResult) string {
	if result.Groups != nil {
		groupsJSON, _ := json.MarshalIndent(result.Groups, "", "  ")
		return fmt.Sprintf("Grouped results:\n%s", string(groupsJSON))
	}

	if result.Values != nil {
		valuesJSON, _ := json.MarshalIndent(result.Values, "", "  ")
		return fmt.Sprintf("Distinct values:\n%s", string(valuesJSON))
	}

	if result.Item != nil {
		itemJSON, _ := json.MarshalIndent(result.Item, "", "  ")
		return fmt.Sprintf("Result: %v\nItem:\n%s", result.Value, string(itemJSON))
	}

	return fmt.Sprintf("Result: %v", result.Value)
}
