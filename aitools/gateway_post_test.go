package aitools_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

type fakeGatewayBridge struct {
	channel string
	text    string
	err     error
}

func (f *fakeGatewayBridge) PostMessage(_ context.Context, channel, text string) error {
	f.channel = channel
	f.text = text
	return f.err
}

var _ = Describe("GatewayPostTool", func() {
	It("returns the no-gateway observation when no bridge is wired", func() {
		t := &aitools.GatewayPostTool{Bridge: nil}
		out := t.Call(context.Background(), `{"message":"hi"}`)
		Expect(out).To(Equal(aitools.NoGatewayObservation))
	})

	It("requires a message", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{}}
		Expect(t.Call(context.Background(), `{}`)).To(ContainSubstring("message is required"))
	})

	It("posts the message (and channel override) through the bridge", func() {
		b := &fakeGatewayBridge{}
		t := &aitools.GatewayPostTool{Bridge: b}
		out := t.Call(context.Background(), `{"message":"deploy done","channel":"#ops"}`)
		Expect(out).To(ContainSubstring("posted"))
		Expect(b.text).To(Equal("deploy done"))
		Expect(b.channel).To(Equal("#ops"))
	})

	It("surfaces a bridge error to the agent", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{err: errors.New("no gateway is currently running")}}
		Expect(t.Call(context.Background(), `{"message":"hi"}`)).To(ContainSubstring("no gateway is currently running"))
	})
})
