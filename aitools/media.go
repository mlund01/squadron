package aitools

import "context"

// MediaKind identifies how a MediaBlock should reach the model.
const (
	MediaKindImage    = "image"
	MediaKindDocument = "document"
)

// MediaBlock is a provider-neutral piece of visual content a tool produces for
// the model — an image or a document (PDF). The agent layer translates it into
// the matching llm content block; aitools stays free of any llm import.
type MediaBlock struct {
	Kind      string // MediaKindImage | MediaKindDocument
	MediaType string // "image/png", "application/pdf", ...
	Data      string // Base64-encoded payload, no data-URL prefix
	Filename  string
}

// MediaTool is an optional interface a Tool implements when a call can return
// visual content (images, documents) alongside its text result. The orchestrator
// type-asserts for it (like OutputSchemaTool) and routes the media into the
// conversation's vision/document channel rather than the text tool_result, so it
// never passes through the result interceptor.
type MediaTool interface {
	Tool
	CallMedia(ctx context.Context, params string) (text string, media []MediaBlock)
}

// MediaToolOf returns the MediaTool view of a tool when it implements one.
func MediaToolOf(t Tool) (MediaTool, bool) {
	mt, ok := t.(MediaTool)
	return mt, ok
}
