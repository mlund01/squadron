package aitools

import (
	"context"
	"encoding/json"
	"strings"
)

// GatewayEditTool replaces a posted message using the gateway's message schema.
type GatewayEditTool struct {
	Bridge GatewayBridge
	Store  MemoryStore
}

func (t *GatewayEditTool) ToolName() string { return "edit" }

const gatewayEditIDProperty = `{"type": "string", "description": "The id builtins.gateway.post returned for the message to change."}`

func (t *GatewayEditTool) ToolDescription() string {
	return "Replace the content of a message posted earlier with builtins.gateway.post. " +
		"`id` is the id that post returned; the other fields are the new content, shaped as for builtins.gateway.post, and replace the whole message. " +
		gatewayPostDescription(t.Bridge, t.Store)
}

func (t *GatewayEditTool) ToolPayloadSchema() Schema {
	raw := injectProperty(gatewayPostSchema(t.Bridge, t.Store), "id", gatewayEditIDProperty, true)
	return Schema{Type: TypeObject, Properties: PropertyMap{}}.WithRawJSONSchema(json.RawMessage(raw))
}

func (t *GatewayEditTool) Call(ctx context.Context, params string) string {
	if t.Bridge == nil {
		return NoGatewayObservation
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &root); err != nil {
		return "Error: invalid JSON parameters"
	}
	var id string
	if raw, ok := root["id"]; ok {
		if err := json.Unmarshal(raw, &id); err != nil {
			return "Error: id must be a string"
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "Error: id is required"
	}
	delete(root, "id")
	if len(root) == 0 {
		return "Error: empty message payload"
	}
	attachments, _, errMsg := takeAttachments(t.Store, root)
	if errMsg != "" {
		return "Error: " + errMsg
	}
	payload, err := json.Marshal(root)
	if err != nil {
		return "Error: " + err.Error()
	}
	if err := t.Bridge.EditMessage(ctx, id, string(payload), attachments); err != nil {
		return "Error: " + err.Error()
	}
	return "Message " + id + " updated."
}
