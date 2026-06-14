package aitools

import (
	"context"
	"encoding/json"
	"strings"
)

// GatewayBridge posts a message through the configured gateway subprocess
// (Discord, Slack, …) and advertises how that gateway wants messages shaped.
// Pass nil to build a tool that reports the gateway is unavailable instead of
// posting — the tool is always registered.
type GatewayBridge interface {
	// PostMessage forwards the raw, gateway-schema-shaped JSON the agent
	// produced. The gateway parses it.
	PostMessage(ctx context.Context, payload string) error
	// MessageToolDescription is the gateway-supplied tool description (how to
	// format messages for this gateway). Empty → squadron's default.
	MessageToolDescription() string
	// MessageToolSchema is the gateway-supplied JSON Schema for the tool's
	// params. Empty → squadron's default { message, channel } shape.
	MessageToolSchema() string
}

// GatewayPostTool backs builtins.gateway.post. The gateway owns the message
// contract: its description and JSON Schema are surfaced to the LLM, and the
// raw params the LLM produces are forwarded to the gateway verbatim.
type GatewayPostTool struct {
	Bridge GatewayBridge
}

func (t *GatewayPostTool) ToolName() string { return "post" }

const defaultGatewayPostDescription = "Post a message to the configured gateway's external system (Discord, Slack, etc.). " +
	"If no gateway is configured, returns \"[no gateway configured]\" so you can proceed without failing."

const defaultGatewayPostSchema = `{
  "type": "object",
  "properties": {
    "message": {"type": "string", "description": "The message text to post."},
    "channel": {"type": "string", "description": "Optional channel name or id override."}
  },
  "required": ["message"]
}`

func (t *GatewayPostTool) ToolDescription() string {
	if t.Bridge != nil {
		if d := t.Bridge.MessageToolDescription(); d != "" {
			return d
		}
	}
	return defaultGatewayPostDescription
}

func (t *GatewayPostTool) ToolPayloadSchema() Schema {
	raw := ""
	if t.Bridge != nil {
		raw = t.Bridge.MessageToolSchema()
	}
	if strings.TrimSpace(raw) == "" {
		raw = defaultGatewayPostSchema
	}
	// The gateway owns the schema; pass it through verbatim so the LLM sees
	// exactly what this gateway accepts.
	return Schema{Type: TypeObject, Properties: PropertyMap{}}.WithRawJSONSchema(json.RawMessage(raw))
}

// NoGatewayObservation is what Call returns when no gateway bridge is wired.
const NoGatewayObservation = "[no gateway configured]"

func (t *GatewayPostTool) Call(ctx context.Context, params string) string {
	if t.Bridge == nil {
		return NoGatewayObservation
	}
	if s := strings.TrimSpace(params); s == "" || s == "{}" {
		return "Error: empty message payload"
	}
	if !json.Valid([]byte(params)) {
		return "Error: invalid JSON parameters"
	}
	if err := t.Bridge.PostMessage(ctx, params); err != nil {
		return "Error: " + err.Error()
	}
	return "Message posted to the gateway."
}
