package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"squad/agent/internal/prompts"
	"squad/aitools"
	"squad/config"
	"squad/llm"
	"squad/streamers"
)

// DependencySummary holds the summary from a completed dependency task
type DependencySummary struct {
	TaskName string
	Summary  string
}

// SupervisorOptions holds configuration for creating a supervisor
type SupervisorOptions struct {
	// Config is the loaded configuration
	Config *config.Config
	// ConfigPath is the path to the config directory (needed for spawning agents)
	ConfigPath string
	// WorkflowName is the name of the workflow
	WorkflowName string
	// TaskName is the name of the task this supervisor is executing
	TaskName string
	// SupervisorModel is the model key for the supervisor (e.g., "claude_sonnet_4")
	SupervisorModel string
	// AgentNames is the list of agents available to this supervisor
	AgentNames []string
	// DepSummaries contains summaries from completed dependency tasks
	DepSummaries []DependencySummary
	// DebugFile enables debug logging to the specified file (optional)
	DebugFile string
}

// SupervisorToolCallbacks allows the workflow to provide callbacks for supervisor tools
type SupervisorToolCallbacks struct {
	// OnAgentStart is called when call_agent begins executing an agent
	OnAgentStart func(taskName, agentName string)
	// GetAgentHandler returns a ChatHandler for the agent execution
	GetAgentHandler func(taskName, agentName string) streamers.ChatHandler
	// OnAgentComplete is called when call_agent finishes executing an agent
	OnAgentComplete func(taskName, agentName string)
	// AskSupervisor is called when ask_supe queries another supervisor
	AskSupervisor func(ctx context.Context, taskName, question string) (string, error)
}

// SupervisorStreamer is the interface for streaming supervisor events
type SupervisorStreamer interface {
	Reasoning(content string)
	Answer(content string)
	CallingTool(name, input string)
	ToolComplete(name string)
}

// Supervisor is an agent specialized for orchestrating other agents in a workflow
type Supervisor struct {
	Name      string
	TaskName  string
	ModelName string

	session      *llm.Session
	tools        map[string]aitools.Tool
	provider     llm.Provider
	ownsProvider bool
	agents       map[string]*config.Agent
	callbacks    *SupervisorToolCallbacks
	configPath   string
	cfg          *config.Config
}

// NewSupervisor creates a new supervisor for a workflow task
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

	// Main supervisor prompt
	systemPrompts = append(systemPrompts, prompts.GetSupervisorPrompt(agentInfos))

	// Add context about workflow and task
	systemPrompts = append(systemPrompts, fmt.Sprintf(
		"You are executing task '%s' in workflow '%s'.",
		opts.TaskName, opts.WorkflowName,
	))

	// Create session
	session := llm.NewSession(provider, actualModelName, systemPrompts...)

	if opts.DebugFile != "" {
		if err := session.EnableDebug(opts.DebugFile); err != nil {
			fmt.Printf("Warning: could not enable debug logging: %v\n", err)
		}
	}

	sup := &Supervisor{
		Name:         fmt.Sprintf("%s/%s", opts.WorkflowName, opts.TaskName),
		TaskName:     opts.TaskName,
		ModelName:    actualModelName,
		session:      session,
		tools:        make(map[string]aitools.Tool),
		provider:     provider,
		ownsProvider: ownsProvider,
		agents:       agents,
		configPath:   opts.ConfigPath,
		cfg:          opts.Config,
	}

	// If there are dependency summaries, add them as a secondary system prompt
	if len(opts.DepSummaries) > 0 {
		sup.injectDependencyContext(opts.DepSummaries)
	}

	return sup, nil
}

// SetToolCallbacks configures the callbacks for supervisor tools
// This must be called before ExecuteTask to enable call_agent and ask_supe
func (s *Supervisor) SetToolCallbacks(callbacks *SupervisorToolCallbacks, depSummaries []DependencySummary) {
	s.callbacks = callbacks

	// Build call_agent tool
	s.tools["call_agent"] = &callAgentTool{
		supervisor: s,
	}

	// Build ask_supe tool if there are dependencies
	if len(depSummaries) > 0 {
		var availableTasks []string
		for _, dep := range depSummaries {
			availableTasks = append(availableTasks, dep.TaskName)
		}
		s.tools["ask_supe"] = &askSupeTool{
			askFunc:        callbacks.AskSupervisor,
			availableTasks: availableTasks,
		}
	}
}

// injectDependencyContext adds a secondary system prompt with dependency summaries
func (s *Supervisor) injectDependencyContext(summaries []DependencySummary) {
	var sb strings.Builder
	sb.WriteString("## Completed Dependency Tasks\n\n")
	sb.WriteString("The following tasks have been completed. Use their summaries for context.\n")
	sb.WriteString("If you need more specific information, use the `ask_supe` tool to query the relevant supervisor.\n\n")

	for _, summary := range summaries {
		sb.WriteString(fmt.Sprintf("### Task: %s\n", summary.TaskName))
		sb.WriteString(fmt.Sprintf("%s\n\n", summary.Summary))
	}

	s.session.AddSystemPrompt(sb.String())
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

		_, err := s.session.SendStream(ctx, currentInput, func(chunk llm.StreamChunk) {
			if chunk.Content != "" {
				parser.ProcessChunk(chunk.Content)
			}
		})

		parser.Finish()

		if err != nil {
			return "", err
		}

		// Capture the answer if one was provided
		if answer := parser.GetAnswer(); answer != "" {
			finalAnswer = answer
		}

		// Check if there's an action to call
		action := parser.GetAction()
		if action == "" {
			break // No tool call, done with this turn
		}

		actionInput := parser.GetActionInput()
		streamer.CallingTool(action, actionInput)

		// Look up the tool
		tool := s.tools[action]
		if tool == nil {
			streamer.ToolComplete(action)
			currentInput = fmt.Sprintf("<OBSERVATION>\nError: Tool '%s' not found. Available tools: call_agent, ask_supe\n</OBSERVATION>", action)
			continue
		}

		// Execute the tool
		result := tool.Call(actionInput)
		streamer.ToolComplete(action)

		// Send observation back to LLM
		currentInput = fmt.Sprintf("<OBSERVATION>\n%s\n</OBSERVATION>", result)
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
func (s *Supervisor) Close() {
	if s.session != nil {
		s.session.Close()
	}
	if s.ownsProvider {
		if closer, ok := s.provider.(interface{ Close() }); ok {
			closer.Close()
		}
	}
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
)

// supervisorParser parses ReAct-formatted streaming output for supervisors
type supervisorParser struct {
	streamer         SupervisorStreamer
	state            supervisorParserState
	buffer           strings.Builder
	reasoningStarted bool
	answerStarted    bool
	actionName       string
	actionInput      string
	answerText       strings.Builder
	reasoningText    strings.Builder
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

// GetAnswer returns the parsed answer text
func (p *supervisorParser) GetAnswer() string {
	return p.answerText.String()
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
		}
	}
}

// =============================================================================
// Supervisor Tools - call_agent and ask_supe
// =============================================================================

// callAgentTool is the tool for delegating work to agents
type callAgentTool struct {
	supervisor *Supervisor
}

func (t *callAgentTool) ToolName() string {
	return "call_agent"
}

func (t *callAgentTool) ToolDescription() string {
	return "Call another agent to perform a subtask. The agent will execute the task and return the result."
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
				Description: "The task description for the agent to execute",
			},
		},
		Required: []string{"name", "task"},
	}
}

func (t *callAgentTool) Call(input string) string {
	var params struct {
		Name string `json:"name"`
		Task string `json:"task"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	agentCfg, ok := t.supervisor.agents[params.Name]
	if !ok {
		var available []string
		for name := range t.supervisor.agents {
			available = append(available, name)
		}
		return fmt.Sprintf("Error: agent '%s' not found. Available agents: %v", params.Name, available)
	}

	// Create and run the agent
	ctx := context.Background()
	mode := config.ModeWorkflow
	a, err := New(ctx, Options{
		ConfigPath: t.supervisor.configPath,
		AgentName:  agentCfg.Name,
		Mode:       &mode,
	})
	if err != nil {
		return fmt.Sprintf("Error creating agent '%s': %v", params.Name, err)
	}
	defer a.Close()

	// Notify that agent is starting and get a streamer for it
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.OnAgentStart != nil {
		t.supervisor.callbacks.OnAgentStart(t.supervisor.TaskName, params.Name)
	}

	var agentHandler streamers.ChatHandler
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.GetAgentHandler != nil {
		agentHandler = t.supervisor.callbacks.GetAgentHandler(t.supervisor.TaskName, params.Name)
	}

	answer, err := a.Chat(ctx, params.Task, agentHandler)

	// Notify that agent is done
	if t.supervisor.callbacks != nil && t.supervisor.callbacks.OnAgentComplete != nil {
		t.supervisor.callbacks.OnAgentComplete(t.supervisor.TaskName, params.Name)
	}

	if err != nil {
		return fmt.Sprintf("Error executing agent '%s': %v", params.Name, err)
	}

	if answer == "" {
		return fmt.Sprintf("Agent '%s' completed but returned no answer.", params.Name)
	}

	return answer
}

// askSupeTool is the tool for querying other supervisors
type askSupeTool struct {
	askFunc        func(ctx context.Context, taskName string, question string) (string, error)
	availableTasks []string
}

func (t *askSupeTool) ToolName() string {
	return "ask_supe"
}

func (t *askSupeTool) ToolDescription() string {
	return fmt.Sprintf("Ask a question to a supervisor of a completed dependency task. Use this to get specific details that aren't in the summary. Available tasks: %v", t.availableTasks)
}

func (t *askSupeTool) ToolPayloadSchema() aitools.Schema {
	return aitools.Schema{
		Type: aitools.TypeObject,
		Properties: aitools.PropertyMap{
			"task_name": {
				Type:        aitools.TypeString,
				Description: "The name of the completed task whose supervisor you want to query",
			},
			"question": {
				Type:        aitools.TypeString,
				Description: "The question to ask the supervisor",
			},
		},
		Required: []string{"task_name", "question"},
	}
}

func (t *askSupeTool) Call(input string) string {
	var params struct {
		TaskName string `json:"task_name"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return fmt.Sprintf("Error: invalid input: %v", err)
	}

	// Check if task is available
	found := false
	for _, task := range t.availableTasks {
		if task == params.TaskName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Sprintf("Error: task '%s' not found in dependencies. Available tasks: %v", params.TaskName, t.availableTasks)
	}

	// Call the supervisor
	ctx := context.Background()
	answer, err := t.askFunc(ctx, params.TaskName, params.Question)
	if err != nil {
		return fmt.Sprintf("Error querying supervisor: %v", err)
	}

	return answer
}
