package aitools_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

var _ = Describe("GatewayLinkTool", func() {
	It("returns the no-gateway observation when no bridge is wired", func() {
		tool := &aitools.GatewayLinkTool{}
		Expect(tool.Call(context.Background(), `{"key":"DEV-123"}`)).To(Equal(aitools.NoGatewayObservation))
	})

	It("requires a key", func() {
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayLinkTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"key":"  "}`)).To(Equal("Error: key is required"))
		Expect(bridge.linkedKey).To(BeEmpty())
	})

	It("passes the key to the gateway", func() {
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayLinkTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"key":"DEV-123"}`)).To(Equal("Run linked to the request for DEV-123."))
		Expect(bridge.linkedKey).To(Equal("DEV-123"))
	})

	It("surfaces a gateway error to the agent", func() {
		bridge := &fakeGatewayBridge{err: errors.New("no request is filed under \"DEV-123\" yet")}
		tool := &aitools.GatewayLinkTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"key":"DEV-123"}`)).To(ContainSubstring("no request is filed"))
	})
})
