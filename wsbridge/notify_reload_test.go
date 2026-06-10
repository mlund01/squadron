package wsbridge

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/config"
)

// Describe blocks in this file are picked up by the existing internal-package
// suite runner in humaninput_test.go (TestWsbridgeHumanInput). Ginkgo only
// allows one RunSpecs per test binary, so we don't add another.

var _ = Describe("Client.NotifyConfigReloaded", func() {
	// readSentEnvelope drains one message from the client's send channel and
	// decodes it as an Envelope. Fails the spec if nothing arrives or the
	// payload is malformed.
	readSentEnvelope := func(c *Client) *protocol.Envelope {
		var raw []byte
		Eventually(c.send, "1s").Should(Receive(&raw))
		var env protocol.Envelope
		Expect(json.Unmarshal(raw, &env)).To(Succeed())
		return &env
	}

	Context("when the client is not connected", func() {
		It("is a silent no-op (no panic, nothing on the wire)", func() {
			c := newBareClient()
			Expect(c.IsConnected()).To(BeFalse())

			Expect(func() { c.NotifyConfigReloaded(nil) }).NotTo(Panic())
			Consistently(c.send, "50ms").ShouldNot(Receive())
		})
	})

	Context("when the client is connected and the reload succeeded", func() {
		It("pushes an unsolicited TypeReloadConfigResult event carrying the current InstanceConfig", func() {
			c := newBareClient()
			c.connected = true
			c.cfgReady = true
			c.cfg = &config.Config{
				Models: []config.Model{{Name: "m1", Provider: "anthropic", APIKey: "k"}},
			}

			c.NotifyConfigReloaded(nil)

			env := readSentEnvelope(c)
			Expect(env.Type).To(Equal(protocol.TypeReloadConfigResult))
			Expect(env.RequestID).To(BeEmpty(), "unsolicited event must not carry a RequestID")

			var payload protocol.ReloadConfigResultPayload
			Expect(protocol.DecodePayload(env, &payload)).To(Succeed())
			Expect(payload.Success).To(BeTrue(), "expected Success=true (error=%q)", payload.Error)
			Expect(payload.Config.Models).To(HaveLen(1))
			Expect(payload.Config.Models[0].Name).To(Equal("m1"))
		})
	})

	Context("when the client is connected and the reload failed", func() {
		It("pushes a TypeReloadConfigResult event carrying the error and Success=false", func() {
			c := newBareClient()
			c.connected = true

			c.NotifyConfigReloaded(fmt.Errorf("invalid HCL: missing closing brace"))

			env := readSentEnvelope(c)
			Expect(env.Type).To(Equal(protocol.TypeReloadConfigResult))

			var payload protocol.ReloadConfigResultPayload
			Expect(protocol.DecodePayload(env, &payload)).To(Succeed())
			Expect(payload.Success).To(BeFalse())
			Expect(payload.Error).To(Equal("invalid HCL: missing closing brace"))
		})
	})
})
