package aitools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// InvocationMetadata identifies one logical tool invocation across retries and
// process restarts. It is carried out-of-band so it is never exposed to, or
// editable by, the model.
type InvocationMetadata struct {
	RunID          string
	TaskName       string
	AttemptID      string
	ToolUseID      string
	IdempotencyKey string
	StartedAt      time.Time
}

// ReplaySafeTool is implemented only by tools that guarantee a repeated call
// with the same invocation identity will not duplicate an external effect.
type ReplaySafeTool interface {
	ToolReplaySafe() bool
}

func IsReplaySafe(tool Tool) bool {
	replaySafe, ok := tool.(ReplaySafeTool)
	return ok && replaySafe.ToolReplaySafe()
}

type invocationMetadataKey struct{}

// WithInvocationMetadata attaches durable invocation identity to a tool call.
func WithInvocationMetadata(ctx context.Context, metadata InvocationMetadata) context.Context {
	return context.WithValue(ctx, invocationMetadataKey{}, metadata)
}

// InvocationMetadataFromContext returns durable invocation identity when the
// call originated from the mission runtime.
func InvocationMetadataFromContext(ctx context.Context) (InvocationMetadata, bool) {
	metadata, ok := ctx.Value(invocationMetadataKey{}).(InvocationMetadata)
	return metadata, ok
}

// StableIdempotencyKey derives an opaque, transport-safe key for one provider
// tool-use. The inputs are persisted mission identities, so the same logical
// call receives the same key after recovery.
func StableIdempotencyKey(runID, attemptID, toolUseID string) string {
	digest := sha256.Sum256([]byte(runID + "\x00" + attemptID + "\x00" + toolUseID))
	return "sq_" + hex.EncodeToString(digest[:])
}
