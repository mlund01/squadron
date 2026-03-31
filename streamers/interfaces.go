package streamers

import "github.com/mlund01/squadron-wire/protocol"

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
	CallingTool(toolCallId string, toolName string, payload string)

	// ToolComplete is called when a tool finishes execution, with the observation result fed to the LLM
	ToolComplete(toolCallId string, toolName string, result string)

	// ReasoningStarted is called when a REASONING block opens
	ReasoningStarted()

	// PublishReasoningChunk is called for each chunk of the REASONING as it streams
	PublishReasoningChunk(chunk string)

	// ReasoningCompleted is called when the REASONING block is complete
	ReasoningCompleted()

	// PublishAnswerChunk is called for each chunk of the ANSWER as it streams
	PublishAnswerChunk(chunk string)

	// FinishAnswer is called when the answer is complete (to print newlines, stop spinner, etc)
	FinishAnswer()

	// AskCommander is called when the agent sends a question/response back to the commander
	AskCommander(content string)

	// CommanderResponse is called when the agent receives a response from the commander
	CommanderResponse(content string)
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
	CommanderReasoningStarted(taskName string)
	CommanderReasoningCompleted(taskName string, content string)
	CommanderAnswer(taskName string, content string)
	CommanderCallingTool(taskName string, toolCallId string, toolName string, input string)
	CommanderToolComplete(taskName string, toolCallId string, toolName string, result string)

	// Compaction events (context window compacted)
	Compaction(taskName string, entity string, inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int)

	// Session turn telemetry
	SessionTurn(data protocol.SessionTurnData)

	// Agent execution events (for streaming agent output during call_agent)
	AgentStarted(taskName string, agentName string, instruction string)
	AgentHandler(taskName string, agentName string) ChatHandler
	AgentCompleted(taskName string, agentName string)

	// Routing events
	RouteChosen(routerTask string, targetTask string, condition string, isMission bool)
}

// IDRegistrar is an optional interface that MissionHandler implementations can
// implement to receive task and session IDs for correlating events.
type IDRegistrar interface {
	SetTaskID(taskName, taskID string)
	SetSessionID(taskName, agentName, sessionID string)
}
