package aitools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LargeResultConfig configures when results are considered "large"
type LargeResultConfig struct {
	ByteThreshold int // Min bytes before interception (default: 8KB)
	ItemThreshold int // Min array items to trigger (default: 20)
	SampleSize    int // Items to show in sample (default: 5)
	PreviewLength int // Chars to show in text preview (default: 500)
}

// DefaultLargeResultConfig returns the default configuration
func DefaultLargeResultConfig() LargeResultConfig {
	return LargeResultConfig{
		ByteThreshold: 8192,
		ItemThreshold: 20,
		SampleSize:    5,
		PreviewLength: 500,
	}
}

// ResultInterceptor processes tool results before sending to LLM
type ResultInterceptor struct {
	store  ResultStore
	config LargeResultConfig
}

// NewResultInterceptor creates a new result interceptor
func NewResultInterceptor(store ResultStore, config LargeResultConfig) *ResultInterceptor {
	return &ResultInterceptor{store: store, config: config}
}

// InterceptResult contains the result of interception
type InterceptResult struct {
	Data     string // The actual data to show (sample/preview or full result)
	Metadata string // Structured metadata (empty if not intercepted)
	ID       string // Result ID (empty if not intercepted)
}

// Intercept checks if result is large and stores if so
func (i *ResultInterceptor) Intercept(toolName, result string) InterceptResult {
	if i.store == nil {
		return InterceptResult{Data: result}
	}

	// Don't re-intercept results from result_* tools - they're meant to fetch full data
	if strings.HasPrefix(toolName, "result_") {
		return InterceptResult{Data: result}
	}

	// Try JSON array first - check item count regardless of byte size
	var arr []any
	if json.Unmarshal([]byte(result), &arr) == nil && len(arr) >= i.config.ItemThreshold {
		stored := StoredResult{
			Type:    ResultTypeArray,
			Size:    len(arr),
			RawData: result,
			Array:   arr,
		}
		id := i.store.Store(toolName, stored)
		data, metadata := i.buildArrayResult(id, arr)
		return InterceptResult{Data: data, Metadata: metadata, ID: id}
	}

	// For non-arrays, apply byte threshold
	if len(result) < i.config.ByteThreshold {
		return InterceptResult{Data: result}
	}

	// Try JSON object
	var obj map[string]any
	if json.Unmarshal([]byte(result), &obj) == nil {
		stored := StoredResult{
			Type:    ResultTypeObject,
			Size:    len(result),
			RawData: result,
			Object:  obj,
		}
		id := i.store.Store(toolName, stored)
		data, metadata := i.buildObjectResult(id, obj, result)
		return InterceptResult{Data: data, Metadata: metadata, ID: id}
	}

	// Plain text
	stored := StoredResult{
		Type:    ResultTypeText,
		Size:    len(result),
		RawData: result,
	}
	id := i.store.Store(toolName, stored)
	data, metadata := i.buildTextResult(id, result)
	return InterceptResult{Data: data, Metadata: metadata, ID: id}
}

func (i *ResultInterceptor) buildArrayResult(id string, arr []any) (data, metadata string) {
	sampleSize := i.config.SampleSize
	if len(arr) < sampleSize {
		sampleSize = len(arr)
	}
	sample := arr[:sampleSize]
	sampleJSON, _ := json.MarshalIndent(sample, "", "  ")

	data = string(sampleJSON)
	metadata = fmt.Sprintf(`type: array
id: %s
partial: true
total_items: %d
shown_items: %d`, id, len(arr), sampleSize)

	return data, metadata
}

func (i *ResultInterceptor) buildObjectResult(id string, obj map[string]any, raw string) (data, metadata string) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	keysJSON, _ := json.Marshal(keys)

	data = fmt.Sprintf("Top-level keys: %s", string(keysJSON))
	metadata = fmt.Sprintf(`type: object
id: %s
partial: true
total_bytes: %d
total_keys: %d`, id, len(raw), len(keys))

	return data, metadata
}

func (i *ResultInterceptor) buildTextResult(id string, text string) (data, metadata string) {
	previewLen := i.config.PreviewLength
	if len(text) < previewLen {
		previewLen = len(text)
	}
	preview := text[:previewLen]
	if len(text) > previewLen {
		preview += "..."
	}

	data = preview
	metadata = fmt.Sprintf(`type: text
id: %s
partial: true
total_bytes: %d
shown_bytes: %d`, id, len(text), previewLen)

	return data, metadata
}
