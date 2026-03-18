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

	// ToolComplete is called when a tool finishes execution, with the observation result fed to the LLM
	ToolComplete(toolName string, result string)

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
	MissionStarted(name string, missionID string, taskCount int)
	MissionCompleted(name string)

	// Task lifecycle
	TaskStarted(taskName string, objective string)
	TaskCompleted(taskName string)
	TaskFailed(taskName string, err error)

	// Task iteration lifecycle (for tasks with iterator)
	TaskIterationStarted(taskName string, totalItems int, parallel bool)
	TaskIterationCompleted(taskName string, completedCount int)

	// Individual iteration events
	IterationStarted(taskName string, index int, objective string)
	IterationCompleted(taskName string, index int)
	IterationFailed(taskName string, index int, err error)
	IterationRetrying(taskName string, index int, attempt int, maxRetries int, err error)
	IterationReasoning(taskName string, index int, content string)
	IterationAnswer(taskName string, index int, content string)

	// Commander events
	CommanderReasoning(taskName string, content string)
	CommanderAnswer(taskName string, content string)
	CommanderCallingTool(taskName string, toolName string, input string)
	CommanderToolComplete(taskName string, toolName string, result string)

	// Compaction events (context window compacted)
	Compaction(taskName string, entity string, inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int)

	// Agent execution events (for streaming agent output during call_agent)
	AgentStarted(taskName string, agentName string)
	AgentHandler(taskName string, agentName string) ChatHandler
	AgentCompleted(taskName string, agentName string)
}

// IDRegistrar is an optional interface that MissionHandler implementations can
// implement to receive task and session IDs for correlating events.
type IDRegistrar interface {
	SetTaskID(taskName, taskID string)
	SetSessionID(taskName, agentName, sessionID string)
}
