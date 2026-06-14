package aitools

import (
	"context"
	"encoding/json"
)

// GatewayBridge posts a message through the configured gateway subprocess
// (Discord, Slack, …). Pass nil to build a tool that reports the gateway is
// unavailable instead of posting — the tool is always registered.
type GatewayBridge interface {
	PostMessage(ctx context.Context, channel, text string) error
}

// GatewayPostTool backs builtins.gateway.post. The Bridge field is nil when no
// gateway is configured.
type GatewayPostTool struct {
	Bridge GatewayBridge
}

func (t *GatewayPostTool) ToolName() string { return "post" }

func (t *GatewayPostTool) ToolDescription() string {
	return "Post a message to the configured gateway's channel (Discord, Slack, etc.). " +
		"Use this to send a heads-up, status update, or summary to the team. " +
		"`message` is the text to post (markdown is supported by most gateways). " +
		"`channel` is optional — a channel name (e.g. \"#ops\") or id to override the gateway's default destination; omit it to post to the default channel. " +
		"If no gateway is configured, returns \"[no gateway configured]\" so you can proceed without failing."
}

func (t *GatewayPostTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"message": {
				Type:        TypeString,
				Description: "The message text to post.",
			},
			"channel": {
				Type:        TypeString,
				Description: "Optional channel override — a name (with or without a leading #) or an id. Omit to post to the gateway's default channel.",
			},
		},
		Required: []string{"message"},
	}
}

type gatewayPostParams struct {
	Message string `json:"message"`
	Channel string `json:"channel,omitempty"`
}

// NoGatewayObservation is what Call returns when no gateway bridge is wired.
const NoGatewayObservation = "[no gateway configured]"

func (t *GatewayPostTool) Call(ctx context.Context, params string) string {
	var p gatewayPostParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}
	if p.Message == "" {
		return "Error: message is required"
	}
	if t.Bridge == nil {
		return NoGatewayObservation
	}
	if err := t.Bridge.PostMessage(ctx, p.Channel, p.Message); err != nil {
		return "Error: " + err.Error()
	}
	return "Message posted to the gateway."
}
