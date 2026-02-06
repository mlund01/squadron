package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DebugLogger captures workflow execution events and LLM messages for debugging
type DebugLogger struct {
	dir        string
	eventsFile *os.File
	mu         sync.Mutex
	enabled    bool

	// Track message files by entity (supervisor/agent)
	messageFiles map[string]*os.File
}

// NewDebugLogger creates a new debug logger that writes to the specified directory
func NewDebugLogger(dir string) (*DebugLogger, error) {
	if dir == "" {
		return &DebugLogger{enabled: false}, nil
	}

	// Create debug directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating debug directory: %w", err)
	}

	// Open events log file
	eventsPath := filepath.Join(dir, "events.log")
	eventsFile, err := os.Create(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("creating events file: %w", err)
	}

	return &DebugLogger{
		dir:          dir,
		eventsFile:   eventsFile,
		enabled:      true,
		messageFiles: make(map[string]*os.File),
	}, nil
}

// Close closes all open files
func (d *DebugLogger) Close() {
	if !d.enabled {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.eventsFile != nil {
		d.eventsFile.Close()
	}

	for _, f := range d.messageFiles {
		f.Close()
	}
}

// IsEnabled returns true if debug logging is enabled
func (d *DebugLogger) IsEnabled() bool {
	return d.enabled
}

// GetDebugDir returns the debug directory path
func (d *DebugLogger) GetDebugDir() string {
	return d.dir
}

// LogEvent logs a programmatic event to events.log
func (d *DebugLogger) LogEvent(eventType string, data map[string]any) {
	if !d.enabled {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	timestamp := time.Now().Format(time.RFC3339Nano)
	entry := map[string]any{
		"timestamp": timestamp,
		"event":     eventType,
	}
	for k, v := range data {
		entry[k] = v
	}

	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		return
	}

	d.eventsFile.WriteString(string(jsonBytes) + "\n")
}

// GetMessageFile returns a file path for logging LLM messages for a specific entity
func (d *DebugLogger) GetMessageFile(entityType, entityName string) string {
	if !d.enabled {
		return ""
	}

	// Sanitize name for filename
	safeName := strings.ReplaceAll(entityName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "[", "_")
	safeName = strings.ReplaceAll(safeName, "]", "")

	filename := fmt.Sprintf("%s_%s.md", entityType, safeName)
	return filepath.Join(d.dir, filename)
}

// GetTurnLogFile returns a .jsonl file path for per-turn session snapshots.
func (d *DebugLogger) GetTurnLogFile(entityType, entityName string) string {
	if !d.enabled {
		return ""
	}

	safeName := strings.ReplaceAll(entityName, "/", "_")
	safeName = strings.ReplaceAll(safeName, "[", "_")
	safeName = strings.ReplaceAll(safeName, "]", "")

	filename := fmt.Sprintf("turns_%s_%s.jsonl", entityType, safeName)
	return filepath.Join(d.dir, filename)
}

// WriteSystemPrompt writes a system prompt to an entity's message file
func (d *DebugLogger) WriteSystemPrompt(entityType, entityName string, prompts []string) {
	if !d.enabled {
		return
	}

	filePath := d.GetMessageFile(entityType, entityName)
	f, err := d.getOrCreateFile(filePath)
	if err != nil {
		return
	}

	timestamp := time.Now().Format(time.RFC3339)
	f.WriteString(fmt.Sprintf("# %s: %s\n\n", entityType, entityName))
	f.WriteString(fmt.Sprintf("*Started: %s*\n\n", timestamp))
	f.WriteString("---\n\n")

	for i, prompt := range prompts {
		f.WriteString(fmt.Sprintf("## System Prompt %d\n\n", i+1))
		f.WriteString("```\n")
		f.WriteString(prompt)
		f.WriteString("\n```\n\n")
	}

	f.WriteString("---\n\n")
	f.WriteString("# Conversation\n\n")
}

// WriteMessage writes a message exchange to an entity's message file
func (d *DebugLogger) WriteMessage(entityType, entityName string, role string, content string) {
	if !d.enabled {
		return
	}

	filePath := d.GetMessageFile(entityType, entityName)
	f, err := d.getOrCreateFile(filePath)
	if err != nil {
		return
	}

	timestamp := time.Now().Format("15:04:05")

	switch role {
	case "user":
		f.WriteString(fmt.Sprintf("## [%s] User\n\n", timestamp))
	case "assistant":
		f.WriteString(fmt.Sprintf("## [%s] Assistant\n\n", timestamp))
	case "tool_call":
		f.WriteString(fmt.Sprintf("### [%s] Tool Call\n\n", timestamp))
	case "tool_result":
		f.WriteString(fmt.Sprintf("### [%s] Tool Result\n\n", timestamp))
	default:
		f.WriteString(fmt.Sprintf("## [%s] %s\n\n", timestamp, role))
	}

	// Wrap in code block if it looks like structured content
	if strings.HasPrefix(content, "{") || strings.HasPrefix(content, "<") {
		f.WriteString("```\n")
		f.WriteString(content)
		f.WriteString("\n```\n\n")
	} else {
		f.WriteString(content)
		f.WriteString("\n\n")
	}
}

func (d *DebugLogger) getOrCreateFile(path string) (*os.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if f, ok := d.messageFiles[path]; ok {
		return f, nil
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	d.messageFiles[path] = f
	return f, nil
}

// Event type constants
const (
	EventWorkflowStarted     = "workflow_started"
	EventWorkflowCompleted   = "workflow_completed"
	EventTaskStarted         = "task_started"
	EventTaskCompleted       = "task_completed"
	EventTaskFailed          = "task_failed"
	EventIterationStarted    = "iteration_started"
	EventIterationCompleted  = "iteration_completed"
	EventIterationFailed     = "iteration_failed"
	EventIterationRetrying   = "iteration_retrying"
	EventAgentStarted        = "agent_started"
	EventAgentCompleted      = "agent_completed"
	EventToolCall            = "tool_call"
	EventToolResult          = "tool_result"
	EventSupervisorReasoning = "supervisor_reasoning"
	EventSupervisorAnswer    = "supervisor_answer"
	EventSupervisorLLMStart  = "supervisor_llm_start"
	EventSupervisorLLMEnd    = "supervisor_llm_end"
	EventAgentLLMStart       = "agent_llm_start"
	EventAgentLLMEnd         = "agent_llm_end"
	EventAgentToolCall       = "agent_tool_call"
	EventAgentToolResult     = "agent_tool_result"
)
