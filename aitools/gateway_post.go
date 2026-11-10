package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// GatewayBridge posts a message through the configured gateway subprocess
// (Discord, Slack, …) and advertises how that gateway wants messages shaped.
// Pass nil to build a tool that reports the gateway is unavailable instead of
// posting — the tool is always registered.
type GatewayBridge interface {
	// PostMessage forwards the payload and attachments to the gateway and
	// returns the message ID. The ID is empty when the gateway omits it.
	PostMessage(ctx context.Context, payload string, attachments []GatewayAttachment) (string, error)
	// MessageToolDescription is the gateway-supplied tool description (how to
	// format messages for this gateway). Empty → squadron's default.
	MessageToolDescription() string
	// MessageToolSchema is the gateway-supplied JSON Schema for the tool's
	// params. Empty → squadron's default { message, channel } shape.
	MessageToolSchema() string
	// EditMessage replaces the message identified by an ID returned from
	// PostMessage, using the same payload and attachment format.
	EditMessage(ctx context.Context, id, payload string, attachments []GatewayAttachment) error
}

// GatewayAttachment is a squadron-local file resolved from the mission's
// memory/scratchpad/packet storage, shipped to the gateway as raw bytes.
type GatewayAttachment struct {
	Filename string
	MimeType string
	Content  []byte
}

// Attachments are sourced from squadron-local files only (never a URL the
// model picks), so there is no SSRF surface. Caps keep a single post within
// the gateway gRPC channel's message-size budget.
const (
	maxAttachmentBytes      = 25 << 20 // 25 MiB per file
	maxTotalAttachmentBytes = 30 << 20 // 30 MiB per post
)

// GatewayPostTool backs builtins.gateway.post. The gateway owns the message
// contract (text + rich layout): its description and JSON Schema are surfaced
// to the LLM and the params are forwarded verbatim. Squadron owns the
// `attachments` field — it resolves each {slot, path} reference against the
// mission's MemoryStore and ships the bytes to the gateway.
type GatewayPostTool struct {
	Bridge GatewayBridge
	Store  MemoryStore
}

func (t *GatewayPostTool) ToolName() string { return "post" }

const defaultGatewayPostDescription = "Post a message to the configured gateway's external system (Discord, Slack, etc.). " +
	"If no gateway is configured, returns \"[no gateway configured]\" so you can proceed without failing."

const attachmentsDescriptionSuffix = "To attach files, set `attachments` to a list of {\"slot\":..., \"path\":...} objects " +
	"referencing squadron's own memory/scratchpad/packet storage (NOT URLs) — squadron reads each file and uploads it."

const defaultGatewayPostSchema = `{
  "type": "object",
  "properties": {
    "message": {"type": "string", "description": "The message text to post."},
    "channel": {"type": "string", "description": "Optional channel name or id override."}
  },
  "required": ["message"]
}`

const attachmentsSchemaProperty = `{
  "type": "array",
  "description": "Optional files to attach, sourced from squadron's own memory/scratchpad/packet storage (NOT URLs). Each item references a local file by slot and path.",
  "items": {
    "type": "object",
    "properties": {
      "slot": {"type": "string", "description": "Slot: \"memory\", \"scratchpad\", a shared-memory name, or \"packet.<name>\"."},
      "path": {"type": "string", "description": "Relative path within the slot, e.g. \"report.pdf\"."}
    },
    "required": ["slot", "path"]
  }
}`

type gatewayAttachmentRef struct {
	Slot string `json:"slot"`
	Path string `json:"path"`
}

func (t *GatewayPostTool) ToolDescription() string {
	return gatewayPostDescription(t.Bridge, t.Store)
}

func (t *GatewayPostTool) ToolPayloadSchema() Schema {
	return Schema{Type: TypeObject, Properties: PropertyMap{}}.WithRawJSONSchema(json.RawMessage(gatewayPostSchema(t.Bridge, t.Store)))
}

func gatewayPostDescription(bridge GatewayBridge, store MemoryStore) string {
	base := defaultGatewayPostDescription
	if bridge != nil {
		if d := bridge.MessageToolDescription(); d != "" {
			base = d
		}
	}
	if store != nil {
		return base + " " + attachmentsDescriptionSuffix
	}
	return base
}

func gatewayPostSchema(bridge GatewayBridge, store MemoryStore) string {
	raw := ""
	if bridge != nil {
		raw = bridge.MessageToolSchema()
	}
	if strings.TrimSpace(raw) == "" {
		raw = defaultGatewayPostSchema
	}
	// The gateway owns text + rich layout; squadron owns attachments (local
	// files), so inject that field only when a memory store is available.
	if store != nil {
		raw = injectAttachmentsProperty(raw)
	}
	return raw
}

// injectAttachmentsProperty adds squadron's `attachments` property to the
// gateway-owned schema. Best-effort: if the schema can't be parsed or already
// defines `attachments`, it is returned unchanged.
func injectAttachmentsProperty(schema string) string {
	return injectProperty(schema, "attachments", attachmentsSchemaProperty, false)
}

// injectProperty adds a property to the schema and marks it required when requested.
// It returns the original schema if parsing fails or the property already exists.
func injectProperty(schema, name, property string, required bool) string {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schema), &root); err != nil {
		return schema
	}
	props := map[string]json.RawMessage{}
	if rawProps, ok := root["properties"]; ok {
		if err := json.Unmarshal(rawProps, &props); err != nil {
			return schema
		}
	}
	if _, exists := props[name]; exists {
		return schema
	}
	props[name] = json.RawMessage(property)
	newProps, err := json.Marshal(props)
	if err != nil {
		return schema
	}
	root["properties"] = newProps
	if required {
		var names []string
		if rawRequired, ok := root["required"]; ok {
			if err := json.Unmarshal(rawRequired, &names); err != nil {
				return schema
			}
		}
		newRequired, err := json.Marshal(append(names, name))
		if err != nil {
			return schema
		}
		root["required"] = newRequired
	}
	out, err := json.Marshal(root)
	if err != nil {
		return schema
	}
	return string(out)
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

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &root); err != nil {
		return "Error: invalid JSON parameters"
	}
	payload := params
	attachments, found, errMsg := takeAttachments(t.Store, root)
	if errMsg != "" {
		return "Error: " + errMsg
	}
	if found {
		rest, err := json.Marshal(root)
		if err != nil {
			return "Error: " + err.Error()
		}
		payload = string(rest)
	}

	id, err := t.Bridge.PostMessage(ctx, payload, attachments)
	if err != nil {
		return "Error: " + err.Error()
	}
	if id == "" {
		return "Message posted to the gateway."
	}
	return "Message posted to the gateway. Its id is " + id + "; pass it to builtins.gateway.edit to change this message."
}

// takeAttachments removes the attachments field from the payload and reads
// the referenced files from local storage. found reports whether the field was present.
func takeAttachments(store MemoryStore, root map[string]json.RawMessage) (attachments []GatewayAttachment, found bool, errMsg string) {
	rawAtt, ok := root["attachments"]
	if !ok {
		return nil, false, ""
	}
	delete(root, "attachments")
	var refs []gatewayAttachmentRef
	if err := json.Unmarshal(rawAtt, &refs); err != nil {
		return nil, true, "attachments must be an array of {slot, path} objects"
	}
	attachments, errMsg = resolveAttachments(store, refs)
	return attachments, true, errMsg
}

func resolveAttachments(store MemoryStore, refs []gatewayAttachmentRef) ([]GatewayAttachment, string) {
	if len(refs) == 0 {
		return nil, ""
	}
	if store == nil {
		return nil, "attachments are not available for this mission (no memory or scratchpad configured)"
	}
	var out []GatewayAttachment
	var total int
	for _, r := range refs {
		if strings.TrimSpace(r.Slot) == "" || strings.TrimSpace(r.Path) == "" {
			return nil, "each attachment needs a non-empty slot and path"
		}
		abs, err := resolveSlotPath(store, r.Slot, r.Path)
		if err != nil {
			return nil, fmt.Sprintf("attachment %s/%s: %v", r.Slot, r.Path, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Sprintf("attachment %s/%s: %v", r.Slot, r.Path, err)
		}
		if info.IsDir() {
			return nil, fmt.Sprintf("attachment %s/%s: is a directory, not a file", r.Slot, r.Path)
		}
		if info.Size() > maxAttachmentBytes {
			return nil, fmt.Sprintf("attachment %s/%s: %d bytes exceeds the %d-byte per-file limit", r.Slot, r.Path, info.Size(), maxAttachmentBytes)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Sprintf("attachment %s/%s: %v", r.Slot, r.Path, err)
		}
		total += len(data)
		if total > maxTotalAttachmentBytes {
			return nil, fmt.Sprintf("total attachment size exceeds the %d-byte per-post limit", maxTotalAttachmentBytes)
		}
		out = append(out, GatewayAttachment{
			Filename: filepath.Base(r.Path),
			MimeType: detectAttachmentMime(r.Path, data),
			Content:  data,
		})
	}
	return out, ""
}

func detectAttachmentMime(name string, data []byte) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return http.DetectContentType(data)
}
