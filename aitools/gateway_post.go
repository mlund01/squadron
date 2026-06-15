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
	// PostMessage forwards the raw, gateway-schema-shaped JSON the agent
	// produced (text + rich layout) plus any squadron-resolved file
	// attachments. The gateway parses the payload and uploads the attachment
	// bytes directly.
	PostMessage(ctx context.Context, payload string, attachments []GatewayAttachment) error
	// MessageToolDescription is the gateway-supplied tool description (how to
	// format messages for this gateway). Empty → squadron's default.
	MessageToolDescription() string
	// MessageToolSchema is the gateway-supplied JSON Schema for the tool's
	// params. Empty → squadron's default { message, channel } shape.
	MessageToolSchema() string
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
	base := defaultGatewayPostDescription
	if t.Bridge != nil {
		if d := t.Bridge.MessageToolDescription(); d != "" {
			base = d
		}
	}
	if t.Store != nil {
		return base + " " + attachmentsDescriptionSuffix
	}
	return base
}

func (t *GatewayPostTool) ToolPayloadSchema() Schema {
	raw := ""
	if t.Bridge != nil {
		raw = t.Bridge.MessageToolSchema()
	}
	if strings.TrimSpace(raw) == "" {
		raw = defaultGatewayPostSchema
	}
	// The gateway owns text + rich layout; squadron owns attachments (local
	// files), so inject that field only when a memory store is available.
	if t.Store != nil {
		raw = injectAttachmentsProperty(raw)
	}
	return Schema{Type: TypeObject, Properties: PropertyMap{}}.WithRawJSONSchema(json.RawMessage(raw))
}

// injectAttachmentsProperty adds squadron's `attachments` property to the
// gateway-owned schema. Best-effort: if the schema can't be parsed or already
// defines `attachments`, it is returned unchanged.
func injectAttachmentsProperty(schema string) string {
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
	if _, exists := props["attachments"]; exists {
		return schema
	}
	props["attachments"] = json.RawMessage(attachmentsSchemaProperty)
	newProps, err := json.Marshal(props)
	if err != nil {
		return schema
	}
	root["properties"] = newProps
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

	payload := params
	var attachments []GatewayAttachment

	// Pull `attachments` out of the payload and resolve it against the mission's
	// local file storage; the gateway never sees the references, only bytes.
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &root); err != nil {
		return "Error: invalid JSON parameters"
	}
	if rawAtt, ok := root["attachments"]; ok {
		delete(root, "attachments")
		var refs []gatewayAttachmentRef
		if err := json.Unmarshal(rawAtt, &refs); err != nil {
			return "Error: attachments must be an array of {slot, path} objects"
		}
		resolved, errMsg := t.resolveAttachments(refs)
		if errMsg != "" {
			return "Error: " + errMsg
		}
		attachments = resolved
		rest, err := json.Marshal(root)
		if err != nil {
			return "Error: " + err.Error()
		}
		payload = string(rest)
	}

	if err := t.Bridge.PostMessage(ctx, payload, attachments); err != nil {
		return "Error: " + err.Error()
	}
	return "Message posted to the gateway."
}

func (t *GatewayPostTool) resolveAttachments(refs []gatewayAttachmentRef) ([]GatewayAttachment, string) {
	if len(refs) == 0 {
		return nil, ""
	}
	if t.Store == nil {
		return nil, "attachments are not available for this mission (no memory or scratchpad configured)"
	}
	var out []GatewayAttachment
	var total int
	for _, r := range refs {
		if strings.TrimSpace(r.Slot) == "" || strings.TrimSpace(r.Path) == "" {
			return nil, "each attachment needs a non-empty slot and path"
		}
		abs, err := resolveSlotPath(t.Store, r.Slot, r.Path)
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
