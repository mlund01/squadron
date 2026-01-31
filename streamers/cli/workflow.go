package cli

import (
	"fmt"
	"strings"
	"sync"

	"squad/streamers"
)

// WorkflowHandler implements streamers.WorkflowHandler for CLI output
type WorkflowHandler struct {
	mu sync.Mutex
}

// NewWorkflowHandler creates a new CLI workflow handler
func NewWorkflowHandler() *WorkflowHandler {
	return &WorkflowHandler{}
}

func (s *WorkflowHandler) WorkflowStarted(name string, taskCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s=== Workflow: %s ===%s\n", ColorBold, ColorCyan, name, ColorReset)
	fmt.Printf("%sTasks: %d%s\n\n", ColorGray, taskCount, ColorReset)
}

func (s *WorkflowHandler) WorkflowCompleted(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s=== Workflow '%s' completed ===%s\n", ColorBold, ColorGreen, name, ColorReset)
}

func (s *WorkflowHandler) TaskStarted(taskName string, objective string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s--- Task: %s ---%s\n", ColorBold, ColorCyan, taskName, ColorReset)
	fmt.Printf("%sObjective: %s%s\n\n", ColorGray, objective, ColorReset)
}

func (s *WorkflowHandler) TaskCompleted(taskName string, answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s[Task '%s' completed]%s\n", ColorBold, ColorGreen, taskName, ColorReset)
	if answer != "" {
		// Show a truncated version of the answer
		truncated := truncate(answer, 300)
		fmt.Printf("%s%s%s\n", ColorGray, truncated, ColorReset)
	}
}

func (s *WorkflowHandler) TaskFailed(taskName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s[Task '%s' FAILED: %v]%s\n", ColorBold, ColorRed, taskName, err, ColorReset)
}

func (s *WorkflowHandler) SupervisorReasoning(taskName string, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] Thinking: %s\n", taskName, truncate(content, 100))
}

func (s *WorkflowHandler) SupervisorAnswer(taskName string, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] Answer:\n%s\n", taskName, content)
}

func (s *WorkflowHandler) SupervisorCallingTool(taskName string, toolName string, input string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] Calling: %s\n", taskName, toolName)
}

func (s *WorkflowHandler) SupervisorToolComplete(taskName string, toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[%s] %s complete\n", taskName, toolName)
}

func (s *WorkflowHandler) AgentStarted(taskName string, agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s[%s] Running agent '%s'...%s\n", ColorLightBrown, taskName, agentName, ColorReset)
}

func (s *WorkflowHandler) AgentHandler(taskName string, agentName string) streamers.ChatHandler {
	return &agentHandler{
		taskName:  taskName,
		agentName: agentName,
		mu:        &s.mu,
	}
}

func (s *WorkflowHandler) AgentCompleted(taskName string, agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("%s[%s] Agent '%s' finished%s\n", ColorLightBrown, taskName, agentName, ColorReset)
}

// Task iteration events

func (s *WorkflowHandler) TaskIterationStarted(taskName string, totalItems int, parallel bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode := "sequential"
	if parallel {
		mode = "parallel"
	}
	fmt.Printf("\n%s%s--- Task: %s (iterating %d items, %s) ---%s\n", ColorBold, ColorCyan, taskName, totalItems, mode, ColorReset)
}

func (s *WorkflowHandler) TaskIterationCompleted(taskName string, completedCount int, workingSummary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n%s%s[Task '%s' iterations completed: %d]%s\n", ColorBold, ColorGreen, taskName, completedCount, ColorReset)
}

func (s *WorkflowHandler) IterationStarted(taskName string, index int, objective string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("\n  [%s][%d] Starting: %s\n", taskName, index, truncate(objective, 80))
}

func (s *WorkflowHandler) IterationCompleted(taskName string, index int, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Completed\n", taskName, index)
}

func (s *WorkflowHandler) IterationFailed(taskName string, index int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] FAILED: %v\n", taskName, index, err)
}

func (s *WorkflowHandler) IterationRetrying(taskName string, index int, attempt int, maxRetries int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Retrying (%d/%d): %v\n", taskName, index, attempt, maxRetries, err)
}

func (s *WorkflowHandler) IterationReasoning(taskName string, index int, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Thinking: %s\n", taskName, index, truncate(content, 80))
}

func (s *WorkflowHandler) IterationAnswer(taskName string, index int, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("  [%s][%d] Answer: %s\n", taskName, index, truncate(content, 100))
}

func (s *WorkflowHandler) SummaryAggregation(taskName string, summaryCount int) {
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

// agentHandler implements streamers.ChatHandler for workflow agent calls
// Shows agent output in light brown with agent name prefix
type agentHandler struct {
	taskName         string
	agentName        string
	mu               *sync.Mutex
	reasoningStarted bool
	answerBuffer     strings.Builder
}

func (s *agentHandler) Welcome(agentName, modelName string) {
	// Not used in workflow context
}

func (s *agentHandler) AwaitClientAnswer() (string, error) {
	// Not used in workflow context - agents run autonomously
	return "", nil
}

func (s *agentHandler) Goodbye() {
	// Not used in workflow context
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
