package wsbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/config"
	"squadron/config/runtimemodels"
	"squadron/config/runtimevars"
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

var _ = Describe("model connection synchronization", func() {
	BeforeEach(func() { runtimemodels.Replace(nil) })

	It("replaces provider credentials and reloads a model_provider allow-list", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "models.hcl"), []byte(`
model_provider "anthropic" { models = ["claude_haiku_4_5"] }
storage { backend = "sqlite" }
`), 0600)).To(Succeed())
		client := NewClient(&config.Config{}, false, "waiting for model connections", dir, nil, "test")
		env, err := protocol.NewEvent("sync_model_connections", map[string]any{"connections": map[string]any{"anthropic": map[string]any{"provider": "anthropic", "apiKey": "runtime-key", "promptCaching": true}}})
		Expect(err).NotTo(HaveOccurred())
		response, err := client.handleSyncModelConnections(env)
		Expect(err).NotTo(HaveOccurred())
		Expect(response).To(BeNil())
		Expect(client.HasConfig()).To(BeTrue())
		connection, ok := runtimemodels.Get("anthropic")
		Expect(ok).To(BeTrue())
		Expect(connection.APIKey).To(Equal("runtime-key"))
	})

	It("is accepted while configuration is waiting for model connections", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "models.hcl"), []byte(`
model_provider "openai" { models = ["gpt_5"] }
storage { backend = "sqlite" }
`), 0600)).To(Succeed())
		client := NewClient(&config.Config{}, false, "waiting for model connections", dir, nil, "test")
		env, err := protocol.NewEvent(protocol.TypeSyncModelConnections, map[string]any{"connections": map[string]any{"openai": map[string]any{"provider": "openai", "apiKey": "runtime-key", "promptCaching": true}}})
		Expect(err).NotTo(HaveOccurred())

		client.dispatch(env)

		Expect(client.HasConfig()).To(BeTrue())
		connection, ok := runtimemodels.Get("openai")
		Expect(ok).To(BeTrue())
		Expect(connection.APIKey).To(Equal("runtime-key"))
		Consistently(client.send, "50ms").ShouldNot(Receive())
	})
})

var _ = Describe("workspace variable synchronization", func() {
	BeforeEach(func() { runtimevars.Replace(nil) })

	It("replaces the runtime snapshot and reloads configuration", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "variables.hcl"), []byte(`
variable "api_key" { secret = true }
variable "region" { default = "local" }
`), 0600)).To(Succeed())
		client := NewClient(&config.Config{}, false, "waiting for variables", dir, nil, "test")
		env, err := protocol.NewEvent("sync_variables", map[string]any{"values": map[string]string{"api_key": "stored"}})
		Expect(err).NotTo(HaveOccurred())

		response, err := client.handleSyncVariables(env)
		Expect(err).NotTo(HaveOccurred())
		Expect(response).To(BeNil())
		Expect(client.HasConfig()).To(BeTrue())
		Expect(config.LoadVars()).To(Equal(map[string]string{"api_key": "stored"}))
	})
})
