package plugin

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	"squadron/aitools"
)

type mediaTestProvider struct {
	result string
	err    error
	ctx    context.Context
}

func (p *mediaTestProvider) Configure(map[string]string) error { return nil }
func (p *mediaTestProvider) Call(ctx context.Context, _ string, _ string) (string, error) {
	p.ctx = ctx
	return p.result, p.err
}

func TestPluginToolPropagatesInvocationMetadataOutOfBand(t *testing.T) {
	t.Parallel()
	provider := &mediaTestProvider{result: "ok"}
	tool := NewPluginTool(provider, &ToolInfo{Name: "mutate"})
	invocation := aitools.InvocationMetadata{
		RunID: "run-1", TaskName: "collect", AttemptID: "attempt-1", ToolUseID: "call-1", IdempotencyKey: "key-1",
	}
	tool.Call(aitools.WithInvocationMetadata(context.Background(), invocation), `{}`)
	md, ok := metadata.FromOutgoingContext(provider.ctx)
	if !ok {
		t.Fatal("plugin call has no outgoing gRPC metadata")
	}
	wants := map[string]string{
		pluginRunIDMetadataKey: "run-1", pluginTaskNameMetadataKey: "collect", pluginAttemptIDMetadataKey: "attempt-1",
		pluginToolUseIDMetadataKey: "call-1", pluginIdempotencyMetadataKey: "key-1",
	}
	for key, want := range wants {
		values := md.Get(key)
		if len(values) != 1 || values[0] != want {
			t.Fatalf("plugin metadata %q = %v, want %q", key, values, want)
		}
	}
}
func (p *mediaTestProvider) GetToolInfo(string) (*ToolInfo, error) { return nil, nil }
func (p *mediaTestProvider) ListTools() ([]*ToolInfo, error)       { return nil, nil }

func TestPluginToolCallMediaPromotesDataURL(t *testing.T) {
	provider := &mediaTestProvider{result: "captured data:image/png;base64,iVBORw0KGgoAAA=="}
	tool := NewPluginTool(provider, &ToolInfo{Name: "screenshot"})

	text, media := tool.CallMedia(context.Background(), `{}`)
	if text != "captured [image]" {
		t.Fatalf("text = %q, want image placeholder", text)
	}
	if len(media) != 1 || media[0].MediaType != "image/png" || media[0].Data != "iVBORw0KGgoAAA==" {
		t.Fatalf("unexpected media: %#v", media)
	}
}
