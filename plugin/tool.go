package plugin

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/metadata"

	"squadron/aitools"
)

const (
	pluginRunIDMetadataKey       = "squadron-run-id"
	pluginTaskNameMetadataKey    = "squadron-task-name"
	pluginAttemptIDMetadataKey   = "squadron-attempt-id"
	pluginToolUseIDMetadataKey   = "squadron-tool-use-id"
	pluginIdempotencyMetadataKey = "squadron-idempotency-key"
)

type PluginTool struct {
	provider ToolProvider
	info     *ToolInfo
}

func NewPluginTool(provider ToolProvider, info *ToolInfo) *PluginTool {
	return &PluginTool{
		provider: provider,
		info:     info,
	}
}

func (t *PluginTool) ToolName() string {
	return t.info.Name
}

func (t *PluginTool) ToolDescription() string {
	return t.info.Description
}

func (t *PluginTool) ToolPayloadSchema() aitools.Schema {
	return t.info.Schema
}

func (t *PluginTool) ToolOutputSchema() json.RawMessage {
	return t.info.OutputSchema
}
func (t *PluginTool) ToolReplaySafe() bool { return t.info.Idempotent }

func (t *PluginTool) Call(ctx context.Context, params string) string {
	ctx = withPluginInvocationMetadata(ctx)
	result, err := t.provider.Call(ctx, t.info.Name, params)
	if err != nil {
		return "error: " + err.Error()
	}
	return result
}

func withPluginInvocationMetadata(ctx context.Context) context.Context {
	invocation, ok := aitools.InvocationMetadataFromContext(ctx)
	if !ok {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx,
		pluginRunIDMetadataKey, invocation.RunID,
		pluginTaskNameMetadataKey, invocation.TaskName,
		pluginAttemptIDMetadataKey, invocation.AttemptID,
		pluginToolUseIDMetadataKey, invocation.ToolUseID,
		pluginIdempotencyMetadataKey, invocation.IdempotencyKey,
	)
}

// CallMedia preserves image data returned by legacy string-based plugins as
// typed media. Plugin authors do not need a second transport contract: data
// URLs and recognized base64 image fields are promoted at this adapter
// boundary, before large-result interception can truncate them.
func (t *PluginTool) CallMedia(ctx context.Context, params string) (string, []aitools.MediaBlock) {
	result := t.Call(ctx, params)
	if len(result) >= len("error: ") && result[:len("error: ")] == "error: " {
		return result, nil
	}

	extracted := aitools.ExtractImages(result)
	media := make([]aitools.MediaBlock, 0, len(extracted.Images))
	for _, image := range extracted.Images {
		media = append(media, aitools.MediaBlock{
			Kind:      aitools.MediaKindImage,
			MediaType: image.MediaType,
			Data:      image.Data,
		})
	}
	return extracted.RemainingText, media
}
