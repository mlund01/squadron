package aitools

import (
	"context"
	"encoding/json"
	"strings"
)

// GatewayLinkTool backs builtins.gateway.link. A gateway that groups messages
// by request (e.g. a Slack thread per ticket) can only place a run's
// questions and notifications by the run, and those carry no request key.
// Linking first lets a run whose first message is a question still land in
// the request it belongs to, rather than opening a separate one.
type GatewayLinkTool struct {
	Bridge GatewayBridge
}

func (t *GatewayLinkTool) ToolName() string { return "link" }

func (t *GatewayLinkTool) ToolDescription() string {
	return "Join this run to the request the gateway already holds under `key` (the request key runs pass to the gateway, e.g. a ticket key), " +
		"so this run's questions, notifications and posts go to that request. Call it before asking or posting anything. " +
		"If no gateway is configured, returns \"[no gateway configured]\" so you can proceed without failing."
}

func (t *GatewayLinkTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"key": {
				Type:        TypeString,
				Description: "The request key, e.g. a ticket key.",
			},
		},
		Required: []string{"key"},
	}
}

func (t *GatewayLinkTool) Call(ctx context.Context, params string) string {
	if t.Bridge == nil {
		return NoGatewayObservation
	}
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid JSON parameters"
	}
	key := strings.TrimSpace(p.Key)
	if key == "" {
		return "Error: key is required"
	}
	if err := t.Bridge.Link(ctx, key); err != nil {
		return "Error: " + err.Error()
	}
	return "Run linked to the request for " + key + "."
}
