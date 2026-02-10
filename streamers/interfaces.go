package streamers

// ChatHandler defines the interface for handling chat I/O
// Different implementations can handle stdout/stdin, SSE, websocket, etc.
type ChatHandler interface {
	// Welcome displays the initial welcome message when chat starts
	Welcome(agentName string, modelName string)

	// AwaitClientAnswer prompts for and reads user input, returns the input and any error
	AwaitClientAnswer() (string, error)

	// Goodbye displays the farewell message when chat ends
	Goodbye()

	// Error displays an error message
	Error(err error)

	// Thinking is called when the agent starts processing
	Thinking()

	// CallingTool is called when the agent invokes a tool
	CallingTool(toolName string, payload string)

	// ToolComplete is called when a tool finishes execution
	ToolComplete(toolName string)

	// PublishReasoningChunk is called for each chunk of the REASONING as it streams
	PublishReasoningChunk(chunk string)

	// FinishReasoning is called when the REASONING block is complete
	FinishReasoning()

	// PublishAnswerChunk is called for each chunk of the ANSWER as it streams
	PublishAnswerChunk(chunk string)

	// FinishAnswer is called when the answer is complete (to print newlines, stop spinner, etc)
	FinishAnswer()
}

// MissionHandler defines the interface for handling mission execution events
type MissionHandler interface {
	// Mission lifecycle
	MissionStarted(name string, taskCount int)
	MissionCompleted(name string)

	// Task lifecycle
	TaskStarted(taskName string, objective string)
	TaskCompleted(taskName string, summary string)
	TaskFailed(taskName string, err error)

	// Task iteration lifecycle (for tasks with iterator)
	TaskIterationStarted(taskName string, totalItems int, parallel bool)
	TaskIterationCompleted(taskName string, completedCount int, workingSummary string)

	// Individual iteration events
	IterationStarted(taskName string, index int, objective string)
	IterationCompleted(taskName string, index int, summary string)
	IterationFailed(taskName string, index int, err error)
	IterationRetrying(taskName string, index int, attempt int, maxRetries int, err error)
	IterationReasoning(taskName string, index int, content string)
	IterationAnswer(taskName string, index int, content string)

	// Summary aggregation events
	SummaryAggregation(taskName string, summaryCount int)

	// Supervisor events
	SupervisorReasoning(taskName string, content string)
	SupervisorAnswer(taskName string, content string)
	SupervisorCallingTool(taskName string, toolName string, input string)
	SupervisorToolComplete(taskName string, toolName string)

	// Agent execution events (for streaming agent output during call_agent)
	AgentStarted(taskName string, agentName string)
	AgentHandler(taskName string, agentName string) ChatHandler
	AgentCompleted(taskName string, agentName string)
}
