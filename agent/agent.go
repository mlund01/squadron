package agent

import (
	"context"
	"fmt"

	"squad/agent/internal/prompts"
	"squad/aitools"
	"squad/config"
	"squad/llm"
	"squad/streamers"
)

// Agent represents a fully initialized agent ready to chat
type Agent struct {
	Name      string
	ModelName string
	Mode      config.AgentMode

	session      *llm.Session
	tools        map[string]aitools.Tool
	provider     llm.Provider
	ownsProvider bool // true if we created the provider and should close it
}

// Options for creating an agent
type Options struct {
	// ConfigPath is the path to the config directory
	ConfigPath string
	// AgentName is the name of the agent to load
	AgentName string
	// Mode overrides the agent's configured mode (optional)
	Mode *config.AgentMode
	// DebugFile enables debug logging to the specified file (optional)
	DebugFile string
}

// New creates a new agent from config
func New(ctx context.Context, opts Options) (*Agent, error) {
	// Load and validate config
	cfg, err := config.LoadAndValidate(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
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
	tools := config.BuildToolsMap(agentCfg.Tools, cfg.CustomTools, cfg.LoadedPlugins)

	// Determine mode
	mode := agentCfg.Mode
	if opts.Mode != nil {
		mode = *opts.Mode
	}

	// Build system prompts
	var systemPrompts []string
	systemPrompts = append(systemPrompts, prompts.GetAgentPrompt(tools, mode))
	systemPrompts = append(systemPrompts,
		fmt.Sprintf("Personality: %s", agentCfg.Personality),
		fmt.Sprintf("Role: %s", agentCfg.Role),
	)

	// Create session
	session := llm.NewSession(provider, actualModelName, systemPrompts...)

	if opts.DebugFile != "" {
		if err := session.EnableDebug(opts.DebugFile); err != nil {
			// Non-fatal, just log warning
			fmt.Printf("Warning: could not enable debug logging: %v\n", err)
		}
	}

	return &Agent{
		Name:         agentCfg.Name,
		ModelName:    actualModelName,
		Mode:         mode,
		session:      session,
		tools:        tools,
		provider:     provider,
		ownsProvider: ownsProvider,
	}, nil
}

// Close releases resources held by the agent
func (a *Agent) Close() {
	if a.session != nil {
		a.session.Close()
	}
	if a.ownsProvider {
		if closer, ok := a.provider.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

// Chat processes a single message and returns the response
// The streamer receives real-time updates during processing
func (a *Agent) Chat(ctx context.Context, input string, streamer streamers.ChatHandler) (string, error) {
	sessionAdapter := llm.NewSessionAdapter(a.session)
	orchestrator := newOrchestrator(sessionAdapter, streamer, a.tools)
	return orchestrator.processTurn(ctx, input)
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
