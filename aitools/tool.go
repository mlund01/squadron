package aitools

// Tool defines the interface for AI agent tools
type Tool interface {
	// ToolName returns the name of the tool
	ToolName() string

	// ToolDescription returns a description of what the tool does
	ToolDescription() string

	// ToolPayloadSchema returns the JSON schema for the tool's input parameters
	ToolPayloadSchema() Schema

	// Call executes the tool with the given parameters and returns a stringified response
	Call(params string) string
}
