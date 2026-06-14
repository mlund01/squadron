package aitools_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

type fakeGatewayBridge struct {
	payload string
	err     error
	desc    string
	schema  string
}

func (f *fakeGatewayBridge) PostMessage(_ context.Context, payload string) error {
	f.payload = payload
	return f.err
}
func (f *fakeGatewayBridge) MessageToolDescription() string { return f.desc }
func (f *fakeGatewayBridge) MessageToolSchema() string      { return f.schema }

var _ = Describe("GatewayPostTool", func() {
	It("returns the no-gateway observation when no bridge is wired", func() {
		t := &aitools.GatewayPostTool{Bridge: nil}
		Expect(t.Call(context.Background(), `{"message":"hi"}`)).To(Equal(aitools.NoGatewayObservation))
	})

	It("rejects an empty payload", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{}}
		Expect(t.Call(context.Background(), `{}`)).To(ContainSubstring("empty message payload"))
	})

	It("forwards the raw payload to the gateway verbatim", func() {
		b := &fakeGatewayBridge{}
		t := &aitools.GatewayPostTool{Bridge: b}
		payload := `{"text":"deploy done","channel":"#ops","embeds":[{"title":"v2"}]}`
		out := t.Call(context.Background(), payload)
		Expect(out).To(ContainSubstring("posted"))
		Expect(b.payload).To(Equal(payload))
	})

	It("surfaces a bridge error to the agent", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{err: errors.New("no gateway is currently running")}}
		Expect(t.Call(context.Background(), `{"text":"hi"}`)).To(ContainSubstring("no gateway is currently running"))
	})

	It("advertises the gateway-supplied description and schema", func() {
		b := &fakeGatewayBridge{
			desc:   "Post to Discord. text supports markdown.",
			schema: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`,
		}
		t := &aitools.GatewayPostTool{Bridge: b}
		Expect(t.ToolDescription()).To(Equal(b.desc))
		raw := t.ToolPayloadSchema().ToJSONSchema()
		Expect(string(raw)).To(ContainSubstring(`"text"`))
	})

	It("falls back to a default schema when the gateway provides none", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{}}
		Expect(string(t.ToolPayloadSchema().ToJSONSchema())).To(ContainSubstring(`"message"`))
	})
})
