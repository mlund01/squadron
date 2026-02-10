package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"

	"squadron/agent/internal/prompts"
	"squadron/aitools"
	"squadron/config"
	"squadron/llm"
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

// SupervisorOptions holds configuration for creating a supervisor
type SupervisorOptions struct {
	// Config is the loaded configuration
	Config *config.Config
	// ConfigPath is the path to the config directory (needed for spawning agents)
	ConfigPath string
	// MissionName is the name of the mission
	MissionName string
	// TaskName is the name of the task this supervisor is executing
	TaskName string
	// SupervisorModel is the model key for the supervisor (e.g., "claude_sonnet_4")
	SupervisorModel string
	// AgentNames is the list of agents available to this supervisor
	AgentNames []string
	// DepSummaries contains summaries from completed dependency tasks
	DepSummaries []DependencySummary
	// DepOutputSchemas contains output schema info for completed dependency tasks
	DepOutputSchemas []DependencyOutputSchema
	// TaskOutputSchema is the output schema for this task (if defined)
	TaskOutputSchema []OutputFieldSchema
	// InheritedAgents contains completed agents from dependency tasks (for ask_agent)
	InheritedAgents map[string]*Agent
	// PrevIterationOutput contains the structured output from the previous iteration (sequential only)
	PrevIterationOutput map[string]any
	// PrevIterationLearnings contains insights and recommendations from the previous iteration (sequential only)
	PrevIterationLearnings map[string]any
	// SecretInfos contains names and descriptions of available secrets (for prompts)
	SecretInfos []SecretInfo
	// SecretValues contains actual secret values for tool call injection
	SecretValues map[string]string
	// IsIteration indicates whether this supervisor is running an iteration of an iterated task
	IsIteration bool
	// IsParallel indicates whether the iteration is running in parallel (only relevant if IsIteration is true)
	IsParallel bool
	// DebugFile enables debug logging to the specified file (optional)
	DebugFile string
	// SequentialDataset contains all items for sequential iteration processing
	// When set, the supervisor handles all items in a single session using dataset_next/dataset_item_complete tools
	SequentialDataset []cty.Value
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
}

// SupervisorToolCallbacks allows the mission to provide callbacks for supervisor tools
type SupervisorToolCallbacks struct {
	// OnAgentStart is called when call_agent begins executing an agent
	OnAgentStart func(taskName, agentName string)
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
	// GetSupervisorForQuery returns an isolated clone of a completed supervisor for querying.
	// The returned supervisor can answer questions without modifying the original's state.
	// The clone is cached per calling supervisor, so follow-up questions build on previous context.
	// For iterated tasks, pass the iteration index (0+). For regular tasks, pass -1.
	GetSupervisorForQuery func(taskName string, iterationIndex int) (*Supervisor, error)

	// ListSupeQuestions returns the list of questions already asked to a dependency task.
	// Used by iterations to see what questions have been asked by other iterations.
	ListSupeQuestions func(taskName string) []string

	// GetSupeAnswer returns the cached answer for a question by index.
	// Blocks until the answer is ready if another iteration is still querying.
	GetSupeAnswer func(taskName string, index int) (string, error)

	// AskSupeWithCache asks a question with deduplication. If the exact question was
	// already asked, returns the cached answer. Otherwise queries and caches.
	// For iterated tasks, pass the iteration index (0+). For regular tasks, pass -1.
	AskSupeWithCache func(targetTask string, iterationIndex int, question string) (string, error)
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
	Summary         string
	IsIterated      bool
	TotalIterations int
	Iterations      []IterationInfo
	Output          map[string]any
}

// IterationInfo represents a single iteration's output
type IterationInfo struct {
	Index   int
	ItemID  string
	Status  string
	Summary string
	Output  map[string]any
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

// SupervisorStreamer is the interface for streaming supervisor events
type SupervisorStreamer interface {
	Reasoning(content string)
	Answer(content string)
	CallingTool(name, input string)
	ToolComplete(name string)
}

// completedAgent stores a completed agent instance for follow-up queries
type completedAgent struct {
	agent     *Agent
	agentID   string
	inherited bool // true if this agent was inherited from a dependency task
}

// Supervisor is an agent specialized for orchestrating other agents in a mission
type Supervisor struct {
	Name      string
	TaskName  string
	ModelName string

	session         *llm.Session
	tools           map[string]aitools.Tool
	provider        llm.Provider
	ownsProvider    bool
	agents          map[string]*config.Agent
	callbacks       *SupervisorToolCallbacks
	configPath      string
	cfg             *config.Config
	resultStore     *aitools.MemoryResultStore
	interceptor     *aitools.ResultInterceptor
	completedAgents map[string]*completedAgent
	agentSessions   map[string]*Agent // Persistent agent sessions by name (for multi-turn interaction)
	agentSeq        int
	debugLogger     DebugLogger
	turnLogger      *llm.TurnLogger
	queryClones     map[string]*Supervisor // Cached clones for ask_supe queries (keyed by target task name)
	secretInfos     []SecretInfo           // Secret names and descriptions for agent prompts
	secretValues    map[string]string      // Actual secret values for tool call injection
	datasetCursor   *aitools.DatasetCursor // Cursor for sequential dataset iteration (nil if not sequential)
}

// NewSupervisor creates a new supervisor for a mission task
func NewSupervisor(ctx context.Context, opts SupervisorOptions) (*Supervisor, error) {
	// Resolve the supervisor model
	modelConfig, actualModelName, err := resolveSupervisorModel(opts.Config, opts.SupervisorModel)
	if err != nil {
		return nil, fmt.Errorf("resolving supervisor model: %w", err)
	}

	if modelConfig.APIKey == "" {
		return nil, fmt.Errorf("API key not set for model '%s'", modelConfig.Name)
	}

	// Create provider
	provider, ownsProvider, err := createSupervisorProvider(ctx, modelConfig)
	if err != nil {
		return nil, fmt.Errorf("creating provider: %w", err)
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

	// Main supervisor prompt with iteration context
	iterationOpts := prompts.IterationOptions{
		IsIteration: opts.IsIteration,
		IsParallel:  opts.IsParallel,
	}
	systemPrompts = append(systemPrompts, prompts.GetSupervisorPrompt(agentInfos, iterationOpts))

	// Add context about mission and task
	systemPrompts = append(systemPrompts, fmt.Sprintf(
		"You are executing task '%s' in mission '%s'.",
		opts.TaskName, opts.MissionName,
	))

	// Create session
	session := llm.NewSession(provider, actualModelName, systemPrompts...)

	// Set stop sequences to prevent LLM from hallucinating observations
	session.SetStopSequences([]string{"___STOP___"})

	if opts.DebugFile != "" {
		if err := session.EnableDebug(opts.DebugFile); err != nil {
			fmt.Printf("Warning: could not enable debug logging: %v\n", err)
		}
	}

	// Create result store and interceptor for large results
	resultStore := aitools.NewMemoryResultStore()
	interceptor := aitools.NewResultInterceptor(resultStore, aitools.DefaultLargeResultConfig())

	// Initialize completedAgents with inherited agents from dependency tasks
	completedAgents := make(map[string]*completedAgent)
	if opts.InheritedAgents != nil {
		for id, a := range opts.InheritedAgents {
			completedAgents[id] = &completedAgent{
				agent:     a,
				agentID:   id,
				inherited: true, // Mark as inherited so we don't close it
			}
		}
	}

	sup := &Supervisor{
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
		completedAgents: completedAgents,
		agentSessions:   make(map[string]*Agent),
		secretInfos:     opts.SecretInfos,
		secretValues:    opts.SecretValues,
	}

	// Add result tools to supervisor's tool map
	sup.tools["result_info"] = &aitools.ResultInfoTool{Store: resultStore}
	sup.tools["result_items"] = &aitools.ResultItemsTool{Store: resultStore}
	sup.tools["result_get"] = &aitools.ResultGetTool{Store: resultStore}
	sup.tools["result_keys"] = &aitools.ResultKeysTool{Store: resultStore}
	sup.tools["result_chunk"] = &aitools.ResultChunkTool{Store: resultStore}

	// If there are dependency summaries or output schemas, add them as a secondary system prompt
	if len(opts.DepSummaries) > 0 || len(opts.DepOutputSchemas) > 0 {
		sup.injectDependencyContext(opts.DepSummaries, opts.DepOutputSchemas)
	}

	// If task has an output schema, inject instructions for structured output
	if len(opts.TaskOutputSchema) > 0 {
		sup.injectOutputSchemaInstructions(opts.TaskOutputSchema)
	}

	// If there's previous iteration output (sequential iterations), inject it
	if len(opts.PrevIterationOutput) > 0 {
		sup.injectPrevIterationOutput(opts.PrevIterationOutput)
	}

	// If there are learnings from previous iteration (sequential iterations), inject them
	if len(opts.PrevIterationLearnings) > 0 {
		sup.injectPrevIterationLearnings(opts.PrevIterationLearnings)
	}

	// If sequential dataset is provided, set up cursor and tools for iterating through items
	if len(opts.SequentialDataset) > 0 {
		sup.datasetCursor = aitools.NewDatasetCursor(opts.TaskName, opts.SequentialDataset)
		sup.tools["dataset_next"] = aitools.NewDatasetNextTool(sup.datasetCursor)
		sup.tools["dataset_item_complete"] = aitools.NewDatasetItemCompleteTool(sup.datasetCursor)
		sup.injectSequentialDatasetInstructions(len(opts.SequentialDataset))
	}

	return sup, nil
}

// SetToolCallbacks configures the callbacks for supervisor tools
// This must be called before ExecuteTask to enable call_agent and ask_agent
func (s *Supervisor) SetToolCallbacks(callbacks *SupervisorToolCallbacks, depSummaries []DependencySummary) {
	s.callbacks = callbacks
	s.debugLogger = callbacks.DebugLogger

	// Create turn logger for supervisor session snapshots
	if s.debugLogger != nil {
		turnLogFile := s.debugLogger.GetTurnLogFile("supervisor", s.TaskName)
		if turnLogFile != "" {
			if tl, err := llm.NewTurnLogger(turnLogFile); err == nil {
				s.turnLogger = tl
			}
		}
	}

	// Build call_agent tool
	s.tools["call_agent"] = &callAgentTool{
		supervisor: s,
	}

	// Build ask_agent tool for querying completed agents
	s.tools["ask_agent"] = &askAgentTool{
		supervisor: s,
	}

	// Add bridge tool if DatasetStore is available
	if callbacks.DatasetStore != nil {
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

	// Add ask_supe tool if GetSupervisorForQuery callback is available
	if callbacks.GetSupervisorForQuery != nil {
		s.tools["ask_supe"] = &askSupeTool{
			supervisor: s,
		}
	}

	// Add iteration-specific tools if callbacks are available
	if callbacks.ListSupeQuestions != nil {
		s.tools["list_supe_questions"] = &listSupeQuestionsTool{
			supervisor: s,
		}
	}
	if callbacks.GetSupeAnswer != nil {
		s.tools["get_supe_answer"] = &getSupeAnswerTool{
			supervisor: s,
		}
	}
}

// injectDependencyContext adds a secondary system prompt with dependency summaries and output schemas
func (s *Supervisor) injectDependencyContext(summaries []DependencySummary, outputSchemas []DependencyOutputSchema) {
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

// injectOutputSchemaInstructions adds instructions for producing structured output
func (s *Supervisor) injectOutputSchemaInstructions(schema []OutputFieldSchema) {
	var sb strings.Builder
	sb.WriteString("## Required Structured Output\n\n")
	sb.WriteString("This task requires structured output. When you provide your final ANSWER, you MUST also include an OUTPUT block with JSON data matching this schema:\n\n")

	sb.WriteString("**Output fields:**\n")
	for _, field := range schema {
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

	sb.WriteString("\n**Format:**\n")
	sb.WriteString("```\n")
	sb.WriteString("<ANSWER>\n")
	sb.WriteString("Your prose summary of what was accomplished...\n")
	sb.WriteString("</ANSWER>\n")
	sb.WriteString("<OUTPUT>\n")
	sb.WriteString("{")

	// Build example JSON
	for i, field := range schema {
		if i > 0 {
			sb.WriteString(", ")
		}
		switch field.Type {
		case "number", "integer":
			sb.WriteString(fmt.Sprintf("\"%s\": 0", field.Name))
		case "boolean":
			sb.WriteString(fmt.Sprintf("\"%s\": false", field.Name))
		default:
			sb.WriteString(fmt.Sprintf("\"%s\": \"value\"", field.Name))
		}
	}

	sb.WriteString("}\n")
	sb.WriteString("</OUTPUT>\n")
	sb.WriteString("```\n\n")
	sb.WriteString("The OUTPUT block must contain valid JSON with the fields listed above.\n")

	s.session.AddSystemPrompt(sb.String())
}

// injectPrevIterationOutput adds context about the previous iteration's output (for sequential iterations)
func (s *Supervisor) injectPrevIterationOutput(prevOutput map[string]any) {
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

// injectPrevIterationLearnings adds insights and recommendations from the previous iteration (for sequential iterations)
func (s *Supervisor) injectPrevIterationLearnings(learnings map[string]any) {
	learningsJSON, err := json.MarshalIndent(learnings, "", "  ")
	if err != nil {
		// Fallback to simple format if marshaling fails
		learningsJSON = []byte(fmt.Sprintf("%v", learnings))
	}

	prompt := fmt.Sprintf(`## Learnings from Previous Iteration

The previous dataset item's processing provided these insights:
%s

These learnings are from processing a DIFFERENT item, not the one you're handling now.
Apply general insights (API behaviors, error handling, etc.) but focus on your current item's specific parameters.
`, string(learningsJSON))

	s.session.AddSystemPrompt(prompt)
}

// GetDatasetResults returns the collected results from sequential dataset processing
// Returns nil if this supervisor is not processing a sequential dataset
func (s *Supervisor) GetDatasetResults() []aitools.DatasetItemResult {
	if s.datasetCursor == nil {
		return nil
	}
	return s.datasetCursor.GetResults()
}

// HasSequentialDataset returns true if this supervisor is processing a sequential dataset
func (s *Supervisor) HasSequentialDataset() bool {
	return s.datasetCursor != nil
}

// injectSequentialDatasetInstructions adds instructions for processing a sequential dataset
func (s *Supervisor) injectSequentialDatasetInstructions(itemCount int) {
	prompt := fmt.Sprintf(`## Sequential Dataset Processing

You have a dataset of %d items to process sequentially in this single session.

**Tools:**
- dataset_next: Get the next item. Returns {"status": "ok", "index": N, "total": M, "item": {...}} or {"status": "exhausted"}
- dataset_item_complete: Submit output for current item. Required before calling dataset_next again.

**Mission:**
1. Call dataset_next to get an item
2. Process the item (delegate to agent, use tools, etc.)
3. Call dataset_item_complete with the output for this item
4. Repeat until dataset_next returns "exhausted"
5. Produce final ANSWER summarizing the batch

You MUST call dataset_item_complete before dataset_next or you will get an error.
`, itemCount)

	s.session.AddSystemPrompt(prompt)
}

// ExecuteTask runs the task objective to completion
func (s *Supervisor) ExecuteTask(ctx context.Context, objective string, streamer SupervisorStreamer) (string, error) {
	currentInput := objective
	var finalAnswer string

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// Create parser for this message
		parser := newSupervisorParser(streamer)

		if s.debugLogger != nil {
			s.debugLogger.LogEvent("supervisor_llm_start", map[string]any{"task": s.TaskName})
		}
		llmStart := time.Now()

		resp, err := s.session.SendStream(ctx, currentInput, func(chunk llm.StreamChunk) {
			if chunk.Content != "" {
				parser.ProcessChunk(chunk.Content)
			}
		})

		if s.debugLogger != nil {
			eventData := map[string]any{
				"task":        s.TaskName,
				"duration_ms": time.Since(llmStart).Milliseconds(),
			}
			if resp != nil {
				eventData["input_tokens"] = resp.Usage.InputTokens
				eventData["output_tokens"] = resp.Usage.OutputTokens
				// Include cache-related tokens if present
				if resp.Usage.CacheCreationInputTokens > 0 {
					eventData["cache_creation_input_tokens"] = resp.Usage.CacheCreationInputTokens
				}
				if resp.Usage.CacheReadInputTokens > 0 {
					eventData["cache_read_input_tokens"] = resp.Usage.CacheReadInputTokens
				}
				if resp.Usage.CachedTokens > 0 {
					eventData["cached_tokens"] = resp.Usage.CachedTokens
				}
			}
			s.debugLogger.LogEvent("supervisor_llm_end", eventData)
		}

		parser.Finish()

		if err != nil {
			return "", err
		}

		// Determine action for turn logging
		action := parser.GetAction()

		// Log turn snapshot
		if s.turnLogger != nil {
			s.turnLogger.LogTurn(action, s.session.SnapshotMessages())
		}

		// Capture the answer if one was provided
		if answer := parser.GetAnswer(); answer != "" {
			finalAnswer = answer
		}

		if action == "" {
			break // No tool call, done with this turn
		}

		actionInput := parser.GetActionInput()
		streamer.CallingTool(action, actionInput)

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
			streamer.ToolComplete(action)
			currentInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found. Available tools: call_agent, ask_agent\n</OBSERVATION>", action)
			continue
		}

		// Execute the tool
		toolStart := time.Now()
		result := tool.Call(actionInput)

		streamer.ToolComplete(action)

		// Log tool result event
		if s.debugLogger != nil {
			s.debugLogger.LogEvent("tool_result", map[string]any{
				"task":        s.TaskName,
				"tool":        action,
				"result":      result,
				"duration_ms": time.Since(toolStart).Milliseconds(),
			})
		}

		// Intercept large results and format observation
		currentInput = s.formatObservation(action, result)
	}

	if s.turnLogger != nil {
		s.turnLogger.Close()
	}

	return finalAnswer, nil
}

// AnswerQuestion handles a follow-up question from another supervisor (via ask_supe)
func (s *Supervisor) AnswerQuestion(ctx context.Context, question string) (string, error) {
	prompt := fmt.Sprintf(`<FOLLOWUP_QUESTION>
Another supervisor is asking for clarification about your completed task.
Question: %s

Please provide a concise, factual answer based on what you learned during your task execution.
</FOLLOWUP_QUESTION>`, question)

	resp, err := s.session.Send(ctx, prompt)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// Close releases resources held by the supervisor
// Note: Only closes agents created by this supervisor, not inherited ones
func (s *Supervisor) Close() {
	// Close any cached query clones (from ask_supe)
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

	// Only close agents that this supervisor created (not inherited)
	for _, ca := range s.completedAgents {
		if ca.agent != nil && !ca.inherited {
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

// GetCompletedAgents returns all completed agents (for inheritance to dependent tasks)
func (s *Supervisor) GetCompletedAgents() map[string]*Agent {
	result := make(map[string]*Agent)
	for id, ca := range s.completedAgents {
		result[id] = ca.agent
	}
	return result
}

// CloneForQuery creates an isolated copy of this supervisor for answering a question.
// The clone has a copy of the session state (conversation history) but operates independently.
// Multiple clones can be created and queried in parallel without affecting each other.
// The clone shares the same provider and completed agents (for ask_agent support).
func (s *Supervisor) CloneForQuery() *Supervisor {
	// Clone the session for isolated query processing
	clonedSession := s.session.Clone()

	// Copy completed agents map (shallow copy - agents are shared for ask_agent)
	completedAgentsCopy := make(map[string]*completedAgent)
	for id, ca := range s.completedAgents {
		completedAgentsCopy[id] = &completedAgent{
			agent:     ca.agent,
			agentID:   ca.agentID,
			inherited: true, // Mark as inherited so clone doesn't close them
		}
	}

	// Create result store and interceptor for the clone
	resultStore := aitools.NewMemoryResultStore()
	interceptor := aitools.NewResultInterceptor(resultStore, aitools.DefaultLargeResultConfig())

	clone := &Supervisor{
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
		agentSeq:        s.agentSeq,
		debugLogger:     nil, // No debug logging for query clones
	}

	// Add result tools
	clone.tools["result_info"] = &aitools.ResultInfoTool{Store: resultStore}
	clone.tools["result_items"] = &aitools.ResultItemsTool{Store: resultStore}
	clone.tools["result_get"] = &aitools.ResultGetTool{Store: resultStore}
	clone.tools["result_keys"] = &aitools.ResultKeysTool{Store: resultStore}
	clone.tools["result_chunk"] = &aitools.ResultChunkTool{Store: resultStore}

	// Add ask_agent tool so the clone can query its agents
	clone.tools["ask_agent"] = &askAgentTool{supervisor: clone}

	return clone
}

// AnswerQueryIsolated answers a follow-up question using an isolated session.
// This is called on a cloned supervisor and does not affect the original.
// It runs an execution loop to handle any tool calls (like ask_agent) before returning.
func (s *Supervisor) AnswerQueryIsolated(ctx context.Context, question string) (string, error) {
	currentInput := fmt.Sprintf(`<FOLLOWUP_QUESTION>
Another supervisor is asking for additional information about your completed task.
Question: %s

You may use ask_agent to query your agents if you need more details from them.
Provide a concise, factual answer based on what you learned during your task execution.
Wrap your final answer in <ANSWER> tags.
</FOLLOWUP_QUESTION>`, question)

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

		result := tool.Call(actionInput)
		currentInput = s.formatObservation(action, result)
	}
}

// parseActionFromContent extracts ACTION and ACTION_INPUT from a response
func (s *Supervisor) parseActionFromContent(content string) (action, actionInput string) {
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
func (s *Supervisor) extractAnswer(content string) string {
	if idx := strings.Index(content, "<ANSWER>"); idx != -1 {
		content = content[idx+8:]
		if endIdx := strings.Index(content, "</ANSWER>"); endIdx != -1 {
			content = content[:endIdx]
		}
	}
	return strings.TrimSpace(content)
}

// formatObservation formats a tool result as an observation, with optional metadata
func (s *Supervisor) formatObservation(toolName, result string) string {
	if s.interceptor == nil {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", result)
	}

	ir := s.interceptor.Intercept(toolName, result)
	if ir.Metadata == "" {
		return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", ir.Data)
	}

	return fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>\n<OBSERVATION_METADATA>\n%s\n</OBSERVATION_METADATA>", ir.Data, ir.Metadata)
}

// ExecuteAggregation performs a simple LLM call for summary aggregation (no tools)
func (s *Supervisor) ExecuteAggregation(ctx context.Context, prompt string) (string, error) {
	resp, err := s.session.Send(ctx, prompt)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// resolveSupervisorModel finds the model config for a model key
func resolveSupervisorModel(cfg *config.Config, modelKey string) (*config.Model, string, error) {
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

// createSupervisorProvider creates the appropriate LLM provider based on config
func createSupervisorProvider(ctx context.Context, modelConfig *config.Model) (llm.Provider, bool, error) {
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
// Supervisor Parser - parses ReAct-formatted streaming output
// =============================================================================

// supervisorParserState represents the current parsing state
type supervisorParserState int

const (
	supervisorStateNone supervisorParserState = iota
	supervisorStateReasoning
	supervisorStateAction
	supervisorStateActionInput
	supervisorStateAnswer
	supervisorStateOutput
)

// supervisorParser parses ReAct-formatted streaming output for supervisors
type supervisorParser struct {
	streamer         SupervisorStreamer
	state            supervisorParserState
	buffer           strings.Builder
	reasoningStarted bool
	answerStarted    bool
	outputStarted    bool
	actionName       string
	actionInput      string
	answerText       strings.Builder
	reasoningText    strings.Builder
	outputText       strings.Builder
}

// newSupervisorParser creates a new parser with the given streamer
func newSupervisorParser(streamer SupervisorStreamer) *supervisorParser {
	return &supervisorParser{
		streamer: streamer,
		state:    supervisorStateNone,
	}
}

// ProcessChunk processes an incoming chunk of streamed content
func (p *supervisorParser) ProcessChunk(chunk string) {
	p.buffer.WriteString(chunk)
	p.processBuffer()
}

// GetAction returns the parsed action name
func (p *supervisorParser) GetAction() string {
	return p.actionName
}

// GetActionInput returns the parsed action input
func (p *supervisorParser) GetActionInput() string {
	return p.actionInput
}

// GetAnswer returns the parsed answer text, including OUTPUT block if present
func (p *supervisorParser) GetAnswer() string {
	answer := p.answerText.String()
	if p.outputText.Len() > 0 {
		// Append the OUTPUT block to the answer so parseOutput can extract it
		answer += "\n<OUTPUT>\n" + p.outputText.String() + "\n</OUTPUT>"
	}
	return answer
}

// Finish signals that streaming is complete
func (p *supervisorParser) Finish() {
	// Nothing special needed
}

func (p *supervisorParser) processBuffer() {
	content := p.buffer.String()

	for {
		switch p.state {
		case supervisorStateNone:
			// Look for opening tags
			if idx := strings.Index(content, "<REASONING>"); idx != -1 {
				p.state = supervisorStateReasoning
				content = content[idx+11:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ACTION>"); idx != -1 {
				p.state = supervisorStateAction
				content = content[idx+8:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ACTION_INPUT>"); idx != -1 {
				p.state = supervisorStateActionInput
				content = content[idx+14:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<ANSWER>"); idx != -1 {
				p.state = supervisorStateAnswer
				content = content[idx+8:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			if idx := strings.Index(content, "<OUTPUT>"); idx != -1 {
				p.state = supervisorStateOutput
				content = content[idx+8:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return

		case supervisorStateReasoning:
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
					p.streamer.Reasoning(p.reasoningText.String())
				}
				p.reasoningText.Reset()
				p.reasoningStarted = false
				p.state = supervisorStateNone
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

		case supervisorStateAction:
			if idx := strings.Index(content, "</ACTION>"); idx != -1 {
				p.actionName = strings.TrimSpace(content[:idx])
				p.state = supervisorStateNone
				content = content[idx+9:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return

		case supervisorStateActionInput:
			if idx := strings.Index(content, "</ACTION_INPUT>"); idx != -1 {
				p.actionInput = strings.TrimSpace(content[:idx])
				p.state = supervisorStateNone
				content = content[idx+15:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			return

		case supervisorStateAnswer:
			if !p.answerStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.answerStarted = true
				}
			}

			if idx := strings.Index(content, "</ANSWER>"); idx != -1 {
				finalContent := strings.TrimRight(content[:idx], "\n")
				if len(finalContent) > 0 {
					p.answerText.WriteString(finalContent)
					p.streamer.Answer(p.answerText.String())
				}
				p.state = supervisorStateNone
				content = content[idx+9:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			// Buffer content for answer
			if len(content) > 9 {
				safeLen := len(content) - 9
				p.answerText.WriteString(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return

		case supervisorStateOutput:
			if !p.outputStarted {
				content = strings.TrimLeft(content, "\n")
				p.buffer.Reset()
				p.buffer.WriteString(content)
				if len(content) > 0 {
					p.outputStarted = true
				}
			}

			if idx := strings.Index(content, "</OUTPUT>"); idx != -1 {
				finalContent := strings.TrimSpace(content[:idx])
				if len(finalContent) > 0 {
					p.outputText.WriteString(finalContent)
				}
				p.outputStarted = false
				p.state = supervisorStateNone
				content = content[idx+9:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
				continue
			}
			// Buffer content for output
			if len(content) > 9 {
				safeLen := len(content) - 9
				p.outputText.WriteString(content[:safeLen])
				content = content[safeLen:]
				p.buffer.Reset()
				p.buffer.WriteString(content)
			}
			return
		}
	}
}

// =============================================================================
// Supervisor Tools - call_agent and ask_agent
// =============================================================================

// callAgentTool is the tool for delegating work to agents
type callAgentTool struct {
	supervisor *Supervisor
}

func (t *callAgentTool) ToolName() string {
	return "call_agent"
}

func (t *callAgentTool) ToolDescription() string {
	return `Call an agent to perform a task or respond to an agent's question.

Use "task" to assign a new task (always starts fresh, ignores any in-flight work).
Use "response" to answer an agent's ASK_SUPE question (agent continues where it left off).

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
				Description: "Response to an agent's ASK_SUPE question. Agent continues from where it left off.",
			},
		},
		Required: []string{"name"},
	}
}

func (t *callAgentTool) Call(input string) string {
	var params struct {
		Name     string `json:"name"`
		Task     string `json:"task"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Invalid input: %v</ERROR>", err)
	}

	// Validate: must have either task or response, not both
	if params.Task == "" && params.Response == "" {
		return "<STATUS>error</STATUS>\n<ERROR>Must provide either 'task' or 'response'</ERROR>"
	}
	if params.Task != "" && params.Response != "" {
		return "<STATUS>error</STATUS>\n<ERROR>Cannot provide both 'task' and 'response'</ERROR>"
	}

	agentCfg, ok := t.supervisor.agents[params.Name]
	if !ok {
		var available []string
		for name := range t.supervisor.agents {
			available = append(available, name)
		}
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Agent '%s' not found. Available agents: %v</ERROR>", params.Name, available)
	}

	ctx := context.Background()

	// Get existing session or create new one
	a, exists := t.supervisor.agentSessions[params.Name]
	if !exists {
		mode := config.ModeMission

		// Get DatasetStore from callbacks if available
		var datasetStore aitools.DatasetStore
		if t.supervisor.callbacks != nil {
			datasetStore = t.supervisor.callbacks.DatasetStore
		}

		// Get debug file and event logger for agent if debug mode is enabled
		var debugFile string
		var turnLogFile string
		var eventLogger EventLogger
		if t.supervisor.debugLogger != nil {
			agentDebugName := fmt.Sprintf("%s_%s_%d", t.supervisor.TaskName, params.Name, t.supervisor.agentSeq+1)
			debugFile = t.supervisor.debugLogger.GetMessageFile("agent", agentDebugName)
			turnLogFile = t.supervisor.debugLogger.GetTurnLogFile("agent", agentDebugName)
			eventLogger = newContextEventLogger(t.supervisor.debugLogger, map[string]any{
				"task":  t.supervisor.TaskName,
				"agent": params.Name,
			})
		}

		var err error
		a, err = New(ctx, Options{
			ConfigPath:   t.supervisor.configPath,
			AgentName:    agentCfg.Name,
			Mode:         &mode,
			DatasetStore: datasetStore,
			SecretInfos:  t.supervisor.secretInfos,
			SecretValues: t.supervisor.secretValues,
			DebugFile:    debugFile,
			TurnLogFile:  turnLogFile,
			EventLogger:  eventLogger,
		})
		if err != nil {
			return fmt.Sprintf("<STATUS>failed</STATUS>\n<ERROR_TYPE>creation_error</ERROR_TYPE>\n<ERROR>Error creating agent '%s': %v</ERROR>\n<RETRYABLE>false</RETRYABLE>", params.Name, err)
		}

		// Store the session for future interactions
		t.supervisor.agentSessions[params.Name] = a
	}

	// Format input based on task vs response
	var agentInput string
	if params.Response != "" {
		// Answering the agent's question - continue where it left off
		agentInput = fmt.Sprintf("<SUPERVISOR_RESPONSE>\n%s\n</SUPERVISOR_RESPONSE>", params.Response)
	} else if exists {
		// New task on existing session - agent should ignore any in-flight work
		agentInput = fmt.Sprintf("<NEW_TASK>\n%s\n</NEW_TASK>", params.Task)
	} else {
		// Fresh agent, first task - no wrapper needed
		agentInput = params.Task
	}

	// Notify that agent is starting and get a streamer for it
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.OnAgentStart != nil {
		t.supervisor.callbacks.OnAgentStart(t.supervisor.TaskName, params.Name)
	}

	var agentHandler streamers.ChatHandler
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.GetAgentHandler != nil {
		agentHandler = t.supervisor.callbacks.GetAgentHandler(t.supervisor.TaskName, params.Name)
	}

	result, err := a.Chat(ctx, agentInput, agentHandler)

	// Notify that agent is done
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.OnAgentComplete != nil {
		t.supervisor.callbacks.OnAgentComplete(t.supervisor.TaskName, params.Name)
	}

	if err != nil {
		// Classify the error
		errType, retryable := classifyAgentError(err)
		return fmt.Sprintf("<STATUS>failed</STATUS>\n<ERROR_TYPE>%s</ERROR_TYPE>\n<ERROR>%v</ERROR>\n<RETRYABLE>%t</RETRYABLE>", errType, err, retryable)
	}

	// Check if agent needs more info from supervisor
	if result.AskSupe != "" {
		return fmt.Sprintf("<STATUS>needs_input</STATUS>\n<AGENT>%s</AGENT>\n<QUESTION>\n%s\n</QUESTION>", params.Name, result.AskSupe)
	}

	// Check if task is complete with an answer
	if result.Complete {
		// Generate agent ID and store the completed agent for follow-up queries
		t.supervisor.agentSeq++
		agentID := fmt.Sprintf("agent_%d_%s", t.supervisor.agentSeq, params.Name)

		// Store locally - this agent was created by this supervisor
		t.supervisor.completedAgents[agentID] = &completedAgent{
			agent:     a,
			agentID:   agentID,
			inherited: false, // This supervisor created this agent
		}

		return fmt.Sprintf("<STATUS>success</STATUS>\n<AGENT_ID>%s</AGENT_ID>\n<ANSWER>\n%s\n</ANSWER>", agentID, result.Answer)
	}

	// Agent didn't complete or ask for input - unusual state
	return fmt.Sprintf("<STATUS>in_progress</STATUS>\n<AGENT>%s</AGENT>\n<NOTE>Agent is still processing. Call again to continue.</NOTE>", params.Name)
}

// classifyAgentError categorizes an error for supervisor decision-making
func classifyAgentError(err error) (errorType string, retryable bool) {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline"):
		return "timeout", true
	case strings.Contains(errStr, "HTTP") || strings.Contains(errStr, "connection") || strings.Contains(errStr, "network"):
		return "tool_error", true
	case strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "429"):
		return "rate_limit", true
	case strings.Contains(errStr, "not found") || strings.Contains(errStr, "404"):
		return "not_found", false
	case strings.Contains(errStr, "unauthorized") || strings.Contains(errStr, "401") || strings.Contains(errStr, "403"):
		return "auth_error", false
	default:
		return "unknown", false
	}
}

// askAgentTool is the tool for querying completed agents
type askAgentTool struct {
	supervisor *Supervisor
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

func (t *askAgentTool) Call(input string) string {
	var params struct {
		AgentID  string `json:"agent_id"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Invalid input: %v</ERROR>", err)
	}

	// Look up the completed agent (includes both this supervisor's agents and inherited ones)
	completed := t.supervisor.completedAgents[params.AgentID]
	if completed == nil {
		// List available agent IDs
		var available []string
		for id := range t.supervisor.completedAgents {
			available = append(available, id)
		}
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Agent '%s' not found. Available agents: %v</ERROR>", params.AgentID, available)
	}

	// Ask the agent the follow-up question
	ctx := context.Background()
	answer, err := completed.agent.AnswerFollowUp(ctx, params.Question)
	if err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Error asking agent: %v</ERROR>", err)
	}

	return fmt.Sprintf("<STATUS>success</STATUS>\n<ANSWER>\n%s\n</ANSWER>", answer)
}

// =============================================================================
// askSupeTool - queries completed supervisors from dependency tasks
// =============================================================================

// askSupeTool is the tool for querying completed supervisors in the dependency lineage
type askSupeTool struct {
	supervisor *Supervisor
}

func (t *askSupeTool) ToolName() string {
	return "ask_supe"
}

func (t *askSupeTool) ToolDescription() string {
	return `Ask a follow-up question to a completed supervisor from a dependency task. Use this when you need more details than what was provided in the task summary.

The queried supervisor will answer from its existing context and can use ask_agent to query its own agents if needed.

**For iterated tasks:** Use the "index" parameter to query a specific iteration's supervisor. Get the index from query_task_output results (each iteration has an "index" field).

**Context behavior:** The first query to a supervisor creates a clone from its completed state. Subsequent queries to the same supervisor build on previous questions and answers, enabling natural follow-up conversations.`
}

func (t *askSupeTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task_name": {
				Type:        aitools.TypeString,
				Description: "The name of the completed dependency task to query",
			},
			"question": {
				Type:        aitools.TypeString,
				Description: "The follow-up question to ask the supervisor",
			},
			"index": {
				Type:        aitools.TypeInteger,
				Description: "For iterated tasks: the iteration index to query (from query_task_output). Omit for regular tasks.",
			},
		},
		Required: []string{"task_name", "question"},
	}
}

func (t *askSupeTool) Call(input string) string {
	var params struct {
		TaskName string `json:"task_name"`
		Question string `json:"question"`
		Index    *int   `json:"index"` // nil for regular tasks, 0+ for iterated tasks
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Invalid input: %v</ERROR>", err)
	}

	// Determine iteration index (-1 for regular tasks)
	iterIndex := -1
	if params.Index != nil {
		iterIndex = *params.Index
	}

	// Use cached query if available (for iteration deduplication)
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.AskSupeWithCache != nil {
		answer, err := t.supervisor.callbacks.AskSupeWithCache(params.TaskName, iterIndex, params.Question)
		if err != nil {
			return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>%v</ERROR>", err)
		}
		return fmt.Sprintf("<STATUS>success</STATUS>\n<TASK>%s</TASK>\n<ANSWER>\n%s\n</ANSWER>", params.TaskName, answer)
	}

	// Fallback to per-supervisor clone logic (for non-iteration contexts)
	if t.supervisor.callbacks == nil || t.supervisor.callbacks.GetSupervisorForQuery == nil {
		return "<STATUS>error</STATUS>\n<ERROR>ask_supe is not available in this context</ERROR>"
	}

	// Check if we already have a cached clone for this target task (and iteration)
	// Each calling supervisor gets one persistent clone per target task/iteration,
	// so follow-up questions build on previous context
	if t.supervisor.queryClones == nil {
		t.supervisor.queryClones = make(map[string]*Supervisor)
	}

	// Cache key includes iteration index for iterated tasks
	cacheKey := params.TaskName
	if iterIndex >= 0 {
		cacheKey = fmt.Sprintf("%s[%d]", params.TaskName, iterIndex)
	}

	supClone, exists := t.supervisor.queryClones[cacheKey]
	if !exists {
		// Create a new clone and cache it
		var err error
		supClone, err = t.supervisor.callbacks.GetSupervisorForQuery(params.TaskName, iterIndex)
		if err != nil {
			return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>%v</ERROR>", err)
		}
		t.supervisor.queryClones[cacheKey] = supClone
	}

	// Ask the question to the cloned supervisor (clone persists for follow-ups)
	ctx := context.Background()
	answer, err := supClone.AnswerQueryIsolated(ctx, params.Question)
	if err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Error querying supervisor: %v</ERROR>", err)
	}

	return fmt.Sprintf("<STATUS>success</STATUS>\n<TASK>%s</TASK>\n<ANSWER>\n%s\n</ANSWER>", params.TaskName, answer)
}

// =============================================================================
// listSupeQuestionsTool - lists questions asked to dependency supervisors
// =============================================================================

// listSupeQuestionsTool shows what questions have been asked to a dependency task by other iterations
type listSupeQuestionsTool struct {
	supervisor *Supervisor
}

func (t *listSupeQuestionsTool) ToolName() string {
	return "list_supe_questions"
}

func (t *listSupeQuestionsTool) ToolDescription() string {
	return `List questions that have been asked to a dependency supervisor by other iterations.

Use this to see what information has already been requested, so you can reuse existing answers instead of asking duplicate questions. Use get_supe_answer to retrieve the answer for a specific question by its index.`
}

func (t *listSupeQuestionsTool) ToolPayloadSchema() aitools.Schema {
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

func (t *listSupeQuestionsTool) Call(input string) string {
	var params struct {
		TaskName string `json:"task_name"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Invalid input: %v</ERROR>", err)
	}

	if t.supervisor.callbacks == nil || t.supervisor.callbacks.ListSupeQuestions == nil {
		return "<STATUS>error</STATUS>\n<ERROR>list_supe_questions is not available in this context</ERROR>"
	}

	questions := t.supervisor.callbacks.ListSupeQuestions(params.TaskName)
	if len(questions) == 0 {
		return fmt.Sprintf("<STATUS>success</STATUS>\n<TASK>%s</TASK>\n<QUESTIONS>No questions have been asked yet.</QUESTIONS>", params.TaskName)
	}

	var sb strings.Builder
	for i, q := range questions {
		sb.WriteString(fmt.Sprintf("%d: %q\n", i, q))
	}

	return fmt.Sprintf("<STATUS>success</STATUS>\n<TASK>%s</TASK>\n<QUESTIONS>\n%s</QUESTIONS>", params.TaskName, sb.String())
}

// =============================================================================
// getSupeAnswerTool - gets cached answer by index
// =============================================================================

// getSupeAnswerTool retrieves a cached answer for a question by its index
type getSupeAnswerTool struct {
	supervisor *Supervisor
}

func (t *getSupeAnswerTool) ToolName() string {
	return "get_supe_answer"
}

func (t *getSupeAnswerTool) ToolDescription() string {
	return `Get the answer for a previously asked question by its index.

Use list_supe_questions first to see available questions and their indices. If the answer is still being fetched by another iteration, this will wait until it's ready.`
}

func (t *getSupeAnswerTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task_name": {
				Type:        aitools.TypeString,
				Description: "The name of the dependency task",
			},
			"index": {
				Type:        aitools.TypeInteger,
				Description: "The index of the question (from list_supe_questions)",
			},
		},
		Required: []string{"task_name", "index"},
	}
}

func (t *getSupeAnswerTool) Call(input string) string {
	var params struct {
		TaskName string `json:"task_name"`
		Index    int    `json:"index"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>Invalid input: %v</ERROR>", err)
	}

	if t.supervisor.callbacks == nil || t.supervisor.callbacks.GetSupeAnswer == nil {
		return "<STATUS>error</STATUS>\n<ERROR>get_supe_answer is not available in this context</ERROR>"
	}

	answer, err := t.supervisor.callbacks.GetSupeAnswer(params.TaskName, params.Index)
	if err != nil {
		return fmt.Sprintf("<STATUS>error</STATUS>\n<ERROR>%v</ERROR>", err)
	}

	return fmt.Sprintf("<STATUS>success</STATUS>\n<TASK>%s</TASK>\n<INDEX>%d</INDEX>\n<ANSWER>\n%s\n</ANSWER>", params.TaskName, params.Index, answer)
}

// =============================================================================
// queryTaskOutputTool - queries completed task outputs
// =============================================================================

// queryTaskOutputTool allows supervisors to query completed dependency task outputs
type queryTaskOutputTool struct {
	store KnowledgeStore
}

func (t *queryTaskOutputTool) ToolName() string {
	return "query_task_output"
}

func (t *queryTaskOutputTool) ToolDescription() string {
	return `Query structured outputs from completed dependency tasks. Returns only the structured data fields defined in the task's output schema.

**Note:** For narrative summaries or detailed explanations, use ask_supe instead.

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

func (t *queryTaskOutputTool) Call(input string) string {
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
	// Only return structured output - summaries are accessed via ask_supe
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
