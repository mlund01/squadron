package config

import (
	"fmt"
	"strings"
)

// AgentMode defines the operational mode of the agent
type AgentMode string

const (
	// ModeChat is for interactive user chat sessions where reasoning is optional
	ModeChat AgentMode = "chat"
	// ModeMission is for automated missions where the agent continuously reasons
	// and acts until the task is complete
	ModeMission AgentMode = "mission"
)

// ReservedBuiltinNamespaces are names reserved for built-in tools (cannot be
// used as plugin or mcp server names). "mcp" itself is reserved so that a
// `plugin "mcp" { ... }` can't shadow the consumer-side namespace.
var ReservedBuiltinNamespaces = []string{"http", "dataset", "utils", "human", "gateway", "mcp"}

// BuiltinTools maps built-in namespaces to their tools.
// These are accessed as builtins.http.get, builtins.http.get, etc.
var BuiltinTools = map[string][]string{
	"http":    {"get", "post", "put", "patch", "delete"},
	"dataset": {"set", "sample", "count"},
	"utils":   {"sleep", "current_time"},
	"human":   {"ask"},
	"gateway": {"post"},
}

// InternalTools is the list of available internal tools (legacy format for backwards compatibility)
// Deprecated: Use BuiltinTools instead
var InternalTools = []string{
	"http_get",
	"http_post",
	"http_put",
	"http_patch",
	"http_delete",
}

// IsReservedBuiltinNamespace checks if a name is reserved for built-in tools (cannot be used as plugin name)
func IsReservedBuiltinNamespace(name string) bool {
	for _, n := range ReservedBuiltinNamespaces {
		if n == name {
			return true
		}
	}
	return false
}

// IsBuiltinTool checks if a tool reference (e.g., "builtins.http.get") is a built-in tool
func IsBuiltinTool(ref string) bool {
	parts := strings.Split(ref, ".")
	if len(parts) != 3 || parts[0] != "builtins" {
		return false
	}
	pluginName := parts[1]
	toolName := parts[2]

	tools, ok := BuiltinTools[pluginName]
	if !ok {
		return false
	}
	for _, t := range tools {
		if t == toolName {
			return true
		}
	}
	return false
}

// IsInternalTool checks if a tool name is a reserved internal tool (legacy format)
// Deprecated: Use IsBuiltinTool instead
func IsInternalTool(name string) bool {
	for _, t := range InternalTools {
		if t == name {
			return true
		}
	}
	return false
}

// Compaction configures context compaction for an agent
type Compaction struct {
	TokenLimit    int `hcl:"token_limit"`    // Trigger compaction when input tokens exceed this
	TurnRetention int `hcl:"turn_retention"` // Keep this many recent turns uncompacted
}

// Pruning configures context pruning for an agent
type Pruning struct {
	// PruneOn: trigger pruning when conversation reaches this many turns (0 = disabled)
	PruneOn int `hcl:"prune_on,optional"`
	// PruneTo: when pruning triggers, reduce conversation to this many turns
	PruneTo int `hcl:"prune_to,optional"`
}

// Agent represents an AI agent configuration
type Agent struct {
	// Source is the original HCL block from this loaded configuration snapshot.
	// It is exposed explicitly by the definition API, not runtime serialization.
	Source *ConfigSource `hcl:"-" json:"-"`

	Name        string   `hcl:"name,label"`
	Model       string   `hcl:"model"`
	Personality string   `hcl:"personality"`
	Role        string   `hcl:"role,optional"`
	Tools       []string `hcl:"tools,optional"`
	Skills      []string `hcl:"-"`

	// Agent-scoped skills (parsed manually)
	LocalSkills []Skill `hcl:"-" json:"localSkills,omitempty"`
	// Memory governs file memory for independent managed-agent sessions.
	// Mission executors never inherit this policy.
	Memory *AgentMemoryPolicy `hcl:"-" json:"memory,omitempty"`

	// Pruning settings (optional block)
	Pruning *Pruning `hcl:"pruning,block"`

	// Compaction settings (optional block)
	Compaction *Compaction `hcl:"compaction,block"`

	// Tool response size limits (optional block)
	ToolResponse *ToolResponseConfig `hcl:"tool_response,block"`

	// Reasoning controls native provider reasoning (extended thinking on
	// Anthropic, reasoning_effort on OpenAI, thinking_config on Gemini).
	// Valid values: "", "low", "medium", "high". Silently no-op on models
	// that don't support native reasoning.
	Reasoning string `hcl:"reasoning,optional"`
}

// AgentMemoryPolicy controls managed-agent file memory. Session memory belongs
// to one conversation; persistent memory is shared only by sessions of the
// same agent owned by the same user. The default is disabled.
type AgentMemoryPolicy struct {
	Mode          string `hcl:"mode" json:"mode"`
	Scope         string `hcl:"scope,optional" json:"scope,omitempty"`
	RetentionDays int    `hcl:"retention_days,optional" json:"retentionDays,omitempty"`
}

func (p *AgentMemoryPolicy) Validate() error {
	if p == nil {
		return nil
	}
	switch p.Mode {
	case "disabled":
		if p.Scope != "" || p.RetentionDays != 0 {
			return fmt.Errorf("disabled memory cannot declare scope or retention_days")
		}
	case "session":
		if p.Scope != "" {
			return fmt.Errorf("session memory cannot declare scope")
		}
		if p.RetentionDays < 1 || p.RetentionDays > 365 {
			return fmt.Errorf("session memory retention_days must be between 1 and 365")
		}
	case "persistent":
		if p.Scope != "owner" {
			return fmt.Errorf("persistent memory requires scope = \"owner\"")
		}
		if p.RetentionDays < 1 || p.RetentionDays > 3650 {
			return fmt.Errorf("persistent memory retention_days must be between 1 and 3650")
		}
	default:
		return fmt.Errorf("memory mode must be disabled, session, or persistent")
	}
	return nil
}

// ToolResponseConfig configures how large tool call responses are handled.
type ToolResponseConfig struct {
	// MaxTokens is the approximate max token count for a tool response before it gets truncated/sampled.
	// Default: 16000. Hard max: 64000. Converted to bytes internally (~4 bytes per token).
	MaxTokens int `hcl:"max_tokens,optional"`
}

const (
	DefaultToolResponseMaxTokens = 16000 // ~64KB — high default
	HardMaxToolResponseTokens    = 64000 // ~256KB — hard ceiling
	bytesPerToken                = 4     // approximate bytes per token
)

// GetToolResponseMaxBytes returns the configured max size in bytes, falling back to default.
func (a *Agent) GetToolResponseMaxBytes() int {
	if a.ToolResponse == nil || a.ToolResponse.MaxTokens <= 0 {
		return DefaultToolResponseMaxTokens * bytesPerToken
	}
	tokens := a.ToolResponse.MaxTokens
	if tokens > HardMaxToolResponseTokens {
		tokens = HardMaxToolResponseTokens
	}
	return tokens * bytesPerToken
}

// GetPruneOn returns the prune_on threshold (0 = disabled)
func (a *Agent) GetPruneOn() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.PruneOn
}

// GetPruneTo returns the prune_to target (0 = disabled)
func (a *Agent) GetPruneTo() int {
	if a.Pruning == nil {
		return 0
	}
	return a.Pruning.PruneTo
}

// Validate checks that the agent configuration is valid
// Note: Toolbox tools are validated in Config.Validate() since we need access to custom tool definitions
func (a *Agent) Validate() error {
	if err := a.Memory.Validate(); err != nil {
		return fmt.Errorf("agent %q memory: %w", a.Name, err)
	}
	normalized, err := NormalizeReasoning(a.Reasoning)
	if err != nil {
		return fmt.Errorf("agent %q: %w", a.Name, err)
	}
	a.Reasoning = normalized
	return nil
}

// ResolveModel finds the Model config that matches this agent's model key
func (a *Agent) ResolveModel(models []Model) (*Model, string, error) {
	// a.Model is the model key (e.g., "claude_sonnet_4" or "gemma4_26b")
	// Search all model configs for one that has this key available
	for i := range models {
		m := &models[i]
		available := m.AvailableModels()
		if apiName, ok := available[a.Model]; ok {
			return m, apiName, nil
		}
	}

	return nil, "", fmt.Errorf("no model config found for model '%s'", a.Model)
}
