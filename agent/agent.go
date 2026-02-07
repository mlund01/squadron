package agent

import (
	"context"
	"fmt"
	"strings"

	"squad/agent/internal/prompts"
	"squad/aitools"
	"squad/config"
	"squad/llm"
	"squad/streamers"
)

// ChatResult represents the outcome of a chat interaction
type ChatResult struct {
	Answer   string // Final answer (if complete)
	AskSupe  string // Question for supervisor (if agent needs input)
	Complete bool   // True if task is done
}

// Agent represents a fully initialized agent ready to chat
type Agent struct {
	Name      string
	ModelName string
	Mode      config.AgentMode

	session        *llm.Session
	tools          map[string]aitools.Tool
	provider       llm.Provider
	ownsProvider   bool // true if we created the provider and should close it
	resultStore    *aitools.MemoryResultStore
	interceptor    *aitools.ResultInterceptor
	pruningManager *llm.PruningManager
	compaction     *CompactionConfig // Compaction settings (nil if disabled)
	eventLogger    EventLogger
	turnLogger     *llm.TurnLogger   // Persists across Chat() calls for consistent turn numbering
	secretInfos    []SecretInfo      // Secret names and descriptions (for prompts)
	secretValues   map[string]string // Actual secret values (for tool call injection)
}

// CompactionConfig holds settings for context compaction
type CompactionConfig struct {
	TokenLimit    int // Trigger compaction when input tokens exceed this threshold
	TurnRetention int // Keep this many recent turns uncompacted
}

// Options for creating an agent
type Options struct {
	// ConfigPath is the path to the config directory
	ConfigPath string
	// Config is the pre-loaded configuration (optional, avoids reloading and shares plugins)
	Config *config.Config
	// AgentName is the name of the agent to load
	AgentName string
	// Mode overrides the agent's configured mode (optional)
	Mode *config.AgentMode
	// DebugFile enables debug logging to the specified file (optional)
	DebugFile string
	// DatasetStore provides access to workflow datasets (optional, for workflow context)
	DatasetStore aitools.DatasetStore
	// EventLogger provides structured event logging (optional, workflow context only)
	EventLogger EventLogger
	// TurnLogFile enables per-turn session snapshots to the specified JSONL file (optional)
	TurnLogFile string
	// SecretInfos contains names and descriptions of available secrets (for prompts)
	SecretInfos []SecretInfo
	// SecretValues contains actual secret values for tool call injection
	SecretValues map[string]string
}

// New creates a new agent from config
func New(ctx context.Context, opts Options) (*Agent, error) {
	// Use provided config or load from path
	var cfg *config.Config
	var err error
	if opts.Config != nil {
		cfg = opts.Config
	} else {
		cfg, err = config.LoadAndValidate(opts.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}
	}

	// Find the agent config
	var agentCfg *config.Agent
	for _, a := range cfg.Agents {
		if a.Name == opts.AgentName {
			agentCfg = &a
			break
		}
	}

	if agentCfg == nil {
		return nil, fmt.Errorf("agent '%s' not found", opts.AgentName)
	}

	// Resolve model from config
	modelConfig, actualModelName, err := agentCfg.ResolveModel(cfg.Models)
	if err != nil {
		return nil, fmt.Errorf("resolving model: %w", err)
	}

	if modelConfig.APIKey == "" {
		return nil, fmt.Errorf("API key not set for model '%s'", modelConfig.Name)
	}

	// Create provider
	provider, ownsProvider, err := createProvider(ctx, modelConfig)
	if err != nil {
		return nil, fmt.Errorf("creating provider: %w", err)
	}

	// Build tools map
	tools := config.BuildToolsMap(agentCfg.Tools, cfg.CustomTools, cfg.LoadedPlugins, opts.DatasetStore)

	// Create result store and interceptor for large results
	resultStore := aitools.NewMemoryResultStore()
	interceptor := aitools.NewResultInterceptor(resultStore, aitools.DefaultLargeResultConfig())

	// Add result tools to agent's tool map
	tools["result_info"] = &aitools.ResultInfoTool{Store: resultStore}
	tools["result_items"] = &aitools.ResultItemsTool{Store: resultStore}
	tools["result_get"] = &aitools.ResultGetTool{Store: resultStore}
	tools["result_keys"] = &aitools.ResultKeysTool{Store: resultStore}
	tools["result_chunk"] = &aitools.ResultChunkTool{Store: resultStore}

	// Add bridge tool if DatasetStore is available (workflow context)
	if opts.DatasetStore != nil {
		tools["result_to_dataset"] = &aitools.ResultToDatasetTool{
			ResultStore:  resultStore,
			DatasetStore: opts.DatasetStore,
		}
	}

	// Determine mode (defaults to chat, can be overridden via Options)
	mode := config.ModeChat
	if opts.Mode != nil {
		mode = *opts.Mode
	}

	// Build system prompts
	var systemPrompts []string

	// Convert SecretInfos to prompts.SecretInfo
	var promptSecrets []prompts.SecretInfo
	for _, s := range opts.SecretInfos {
		promptSecrets = append(promptSecrets, prompts.SecretInfo{
			Name:        s.Name,
			Description: s.Description,
		})
	}
	systemPrompts = append(systemPrompts, prompts.GetAgentPrompt(tools, mode, promptSecrets))
	systemPrompts = append(systemPrompts,
		fmt.Sprintf("Personality: %s", agentCfg.Personality),
		fmt.Sprintf("Role: %s", agentCfg.Role),
	)

	// Add dataset info if running in workflow context
	if opts.DatasetStore != nil {
		if datasetPrompt := formatDatasetInfo(opts.DatasetStore.GetDatasetInfo()); datasetPrompt != "" {
			systemPrompts = append(systemPrompts, datasetPrompt)
		}
	}

	// Create session
	session := llm.NewSession(provider, actualModelName, systemPrompts...)

	// Set stop sequences to prevent LLM from hallucinating observations
	session.SetStopSequences([]string{"___STOP___"})

	if opts.DebugFile != "" {
		if err := session.EnableDebug(opts.DebugFile); err != nil {
			// Non-fatal, just log warning
			fmt.Printf("Warning: could not enable debug logging: %v\n", err)
		}
	}

	// Create pruning manager tied to this session
	pruningManager := llm.NewPruningManager(
		session,
		agentCfg.GetSingleToolLimit(),
		agentCfg.GetAllToolLimit(),
		agentCfg.GetTurnLimit(),
	)

	// Extract compaction settings from config (if present)
	var compaction *CompactionConfig
	if agentCfg.Compaction != nil {
		compaction = &CompactionConfig{
			TokenLimit:    agentCfg.Compaction.TokenLimit,
			TurnRetention: agentCfg.Compaction.TurnRetention,
		}
	}

	// Create turn logger if path provided (persists across Chat() calls)
	var turnLogger *llm.TurnLogger
	if opts.TurnLogFile != "" {
		if tl, err := llm.NewTurnLogger(opts.TurnLogFile); err == nil {
			turnLogger = tl
		}
	}

	return &Agent{
		Name:           agentCfg.Name,
		ModelName:      actualModelName,
		Mode:           mode,
		session:        session,
		tools:          tools,
		provider:       provider,
		ownsProvider:   ownsProvider,
		resultStore:    resultStore,
		interceptor:    interceptor,
		pruningManager: pruningManager,
		compaction:     compaction,
		eventLogger:    opts.EventLogger,
		turnLogger:     turnLogger,
		secretInfos:    opts.SecretInfos,
		secretValues:   opts.SecretValues,
	}, nil
}

// Close releases resources held by the agent
func (a *Agent) Close() {
	if a.session != nil {
		a.session.Close()
	}
	if a.turnLogger != nil {
		a.turnLogger.Close()
	}
	if a.ownsProvider {
		if closer, ok := a.provider.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

// Chat processes a single message and returns a ChatResult
// The streamer receives real-time updates during processing
func (a *Agent) Chat(ctx context.Context, input string, streamer streamers.ChatHandler) (ChatResult, error) {
	sessionAdapter := llm.NewSessionAdapter(a.session)
	orchestrator := newOrchestrator(sessionAdapter, streamer, a.tools, a.interceptor, a.pruningManager, a.eventLogger, a.turnLogger, a.secretValues, a.compaction)
	return orchestrator.processTurn(ctx, input)
}

// AnswerFollowUp handles a follow-up question using the agent's existing conversation context.
// The agent answers from memory without executing any tool calls.
func (a *Agent) AnswerFollowUp(ctx context.Context, question string) (string, error) {
	prompt := fmt.Sprintf(`<FOLLOWUP_QUESTION>%s</FOLLOWUP_QUESTION>

Answer this question based on your previous work. Do not use any tools.
Provide a direct, factual answer wrapped in <ANSWER> tags.`, question)

	resp, err := a.session.Send(ctx, prompt)
	if err != nil {
		return "", err
	}

	// Parse the answer from the response
	content := resp.Content
	if idx := strings.Index(content, "<ANSWER>"); idx != -1 {
		content = content[idx+8:]
		if endIdx := strings.Index(content, "</ANSWER>"); endIdx != -1 {
			content = content[:endIdx]
		}
	}

	return strings.TrimSpace(content), nil
}

// GetTools returns the agent's available tools
func (a *Agent) GetTools() map[string]aitools.Tool {
	return a.tools
}

// createProvider creates the appropriate LLM provider based on config
func createProvider(ctx context.Context, modelConfig *config.Model) (llm.Provider, bool, error) {
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
		return provider, true, nil // Gemini provider needs to be closed
	default:
		return nil, false, fmt.Errorf("unknown provider: %s", modelConfig.Provider)
	}
}

// formatDatasetInfo creates a system prompt section describing available datasets
func formatDatasetInfo(datasets []aitools.DatasetInfo) string {
	if len(datasets) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Datasets\n\n")
	sb.WriteString("You have access to the following datasets. Use the dataset tools (set_dataset, dataset_sample, dataset_count) to interact with them.\n\n")

	for _, ds := range datasets {
		sb.WriteString(fmt.Sprintf("### %s\n", ds.Name))
		if ds.Description != "" {
			sb.WriteString(fmt.Sprintf("%s\n", ds.Description))
		}
		sb.WriteString(fmt.Sprintf("Current items: %d\n", ds.ItemCount))

		if len(ds.Schema) > 0 {
			sb.WriteString("Schema:\n")
			for _, field := range ds.Schema {
				req := ""
				if field.Required {
					req = " (required)"
				}
				sb.WriteString(fmt.Sprintf("  - %s: %s%s\n", field.Name, field.Type, req))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
