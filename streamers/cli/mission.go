package cli

import (
	"fmt"
	"strings"
	"sync"

	"squadron/streamers"
)

// MissionHandler implements streamers.MissionHandler for CLI output
type MissionHandler struct {
	mu sync.Mutex
}

// NewMissionHandler creates a new CLI mission handler
func NewMissionHandler() *MissionHandler {
	return &MissionHandler{}
}

func (s *MissionHandler) MissionStarted(name string, missionID string, taskCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s=== Mission: %s ===%s\n", ColorBold, ColorCyan, name, ColorReset)
	fmt.Printf("%sMission ID: %s%s\n", ColorGray, missionID, ColorReset)
	fmt.Printf("%sTasks: %d%s\n\n", ColorGray, taskCount, ColorReset)
}

func (s *MissionHandler) MissionCompleted(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s=== Mission '%s' completed ===%s\n", ColorBold, ColorGreen, name, ColorReset)
}

func (s *MissionHandler) TaskStarted(taskName string, objective string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s--- Task: %s ---%s\n", ColorBold, ColorCyan, taskName, ColorReset)
	fmt.Printf("%sObjective: %s%s\n\n", ColorGray, objective, ColorReset)
}

func (s *MissionHandler) TaskCompleted(taskName string, answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s[Task '%s' completed]%s\n", ColorBold, ColorGreen, taskName, ColorReset)
	if answer != "" {
		// Show a truncated version of the answer
		truncated := truncate(answer, 300)
		fmt.Printf("%s%s%s\n", ColorGray, truncated, ColorReset)
	}
}

func (s *MissionHandler) TaskFailed(taskName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s[Task '%s' FAILED: %v]%s\n", ColorBold, ColorRed, taskName, err, ColorReset)
}

func (s *MissionHandler) CommanderReasoning(taskName string, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] Thinking: %s\n", taskName, truncate(content, 100))
}

func (s *MissionHandler) CommanderAnswer(taskName string, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] Answer:\n%s\n", taskName, content)
}

func (s *MissionHandler) CommanderCallingTool(taskName string, toolName string, input string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] Calling: %s\n", taskName, toolName)
}

func (s *MissionHandler) CommanderToolComplete(taskName string, toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] %s complete\n", taskName, toolName)
}

func (s *MissionHandler) AgentStarted(taskName string, agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s[%s] Running agent '%s'...%s\n", ColorLightBrown, taskName, agentName, ColorReset)
}

func (s *MissionHandler) AgentHandler(taskName string, agentName string) streamers.ChatHandler {
	return &agentHandler{
		taskName:  taskName,
		agentName: agentName,
		mu:        &s.mu,
	}
}

func (s *MissionHandler) AgentCompleted(taskName string, agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s[%s] Agent '%s' finished%s\n", ColorLightBrown, taskName, agentName, ColorReset)
}

// Task iteration events

func (s *MissionHandler) TaskIterationStarted(taskName string, totalItems int, parallel bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode := "sequential"
	if parallel {
		mode = "parallel"
	}
	fmt.Printf("\n%s%s--- Task: %s (iterating %d items, %s) ---%s\n", ColorBold, ColorCyan, taskName, totalItems, mode, ColorReset)
}

func (s *MissionHandler) TaskIterationCompleted(taskName string, completedCount int, workingSummary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s[Task '%s' iterations completed: %d]%s\n", ColorBold, ColorGreen, taskName, completedCount, ColorReset)
}

func (s *MissionHandler) IterationStarted(taskName string, index int, objective string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n  [%s][%d] Starting: %s\n", taskName, index, truncate(objective, 80))
}

func (s *MissionHandler) IterationCompleted(taskName string, index int, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Completed\n", taskName, index)
}

func (s *MissionHandler) IterationFailed(taskName string, index int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] FAILED: %v\n", taskName, index, err)
}

func (s *MissionHandler) IterationRetrying(taskName string, index int, attempt int, maxRetries int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Retrying (%d/%d): %v\n", taskName, index, attempt, maxRetries, err)
}

func (s *MissionHandler) IterationReasoning(taskName string, index int, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Thinking: %s\n", taskName, index, truncate(content, 80))
}

func (s *MissionHandler) IterationAnswer(taskName string, index int, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Answer: %s\n", taskName, index, truncate(content, 100))
}

func (s *MissionHandler) SummaryAggregation(taskName string, summaryCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s] Aggregating %d summaries...\n", taskName, summaryCount)
}

// truncate shortens a string to max length, adding ellipsis if needed
func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// agentHandler implements streamers.ChatHandler for mission agent calls
// Shows agent output in light brown with agent name prefix
type agentHandler struct {
	taskName         string
	agentName        string
	mu               *sync.Mutex
	reasoningStarted bool
	answerBuffer     strings.Builder
}

func (s *agentHandler) Welcome(agentName, modelName string) {
	// Not used in mission context
}

func (s *agentHandler) AwaitClientAnswer() (string, error) {
	// Not used in mission context - agents run autonomously
	return "", nil
}

func (s *agentHandler) Goodbye() {
	// Not used in mission context
}

func (s *agentHandler) Error(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s    [%s/%s] Error: %v%s\n", ColorLightBrown, s.taskName, s.agentName, err, ColorReset)
}

func (s *agentHandler) Thinking() {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s    [%s/%s] Thinking...%s\n", ColorLightBrown, s.taskName, s.agentName, ColorReset)
}

func (s *agentHandler) CallingTool(toolName, payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s    [%s/%s] Calling %s...%s\n", ColorLightBrown, s.taskName, s.agentName, toolName, ColorReset)
}

func (s *agentHandler) ToolComplete(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s    [%s/%s] %s complete%s\n", ColorLightBrown, s.taskName, s.agentName, toolName, ColorReset)
}

func (s *agentHandler) PublishReasoningChunk(chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.reasoningStarted {
		fmt.Printf("%s    [%s/%s] Reasoning: ", ColorLightBrown, s.taskName, s.agentName)
		s.reasoningStarted = true
	}
	// Stream reasoning inline in light brown italic
	fmt.Printf("%s%s", ColorItalic, chunk)
}

func (s *agentHandler) FinishReasoning() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reasoningStarted {
		fmt.Printf("%s\n", ColorReset)
		s.reasoningStarted = false
	}
}

func (s *agentHandler) PublishAnswerChunk(chunk string) {
	// Buffer the answer
	s.answerBuffer.WriteString(chunk)
}

func (s *agentHandler) FinishAnswer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	answer := s.answerBuffer.String()
	if answer != "" {
		// Show a truncated version of the answer
		truncated := truncate(answer, 200)
		fmt.Printf("%s    [%s/%s] Answer: %s%s\n", ColorLightBrown, s.taskName, s.agentName, truncated, ColorReset)
	}
	s.answerBuffer.Reset()
}
