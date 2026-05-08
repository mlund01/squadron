package aitools

import (
	"context"
	"encoding/json"
)

type Tool interface {
	ToolName() string
	ToolDescription() string
	ToolPayloadSchema() Schema
	Call(ctx context.Context, params string) string
}

type OutputSchemaTool interface {
	Tool
	ToolOutputSchema() json.RawMessage
}

func ToolOutputSchemaOf(t Tool) json.RawMessage {
	if os, ok := t.(OutputSchemaTool); ok {
		return os.ToolOutputSchema()
	}
	return nil
}
