package aitools_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

var _ = Describe("GatewayEditTool", func() {
	It("returns the no-gateway observation when no bridge is wired", func() {
		tool := &aitools.GatewayEditTool{}
		Expect(tool.Call(context.Background(), `{"id":"C1/1","text":"done"}`)).To(Equal(aitools.NoGatewayObservation))
	})

	It("requires an id", func() {
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayEditTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"text":"done"}`)).To(Equal("Error: id is required"))
		Expect(bridge.editedID).To(BeEmpty())
	})

	It("requires new content", func() {
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayEditTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"id":"C1/1"}`)).To(Equal("Error: empty message payload"))
		Expect(bridge.editedID).To(BeEmpty())
	})

	It("sends the id and the payload without it to the gateway", func() {
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayEditTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"id":"C1/1","text":"done","blocks":[{"type":"divider"}]}`)).To(Equal("Message C1/1 updated."))
		Expect(bridge.editedID).To(Equal("C1/1"))
		Expect(bridge.payload).To(MatchJSON(`{"text":"done","blocks":[{"type":"divider"}]}`))
	})

	It("resolves attachments like a post and strips them from the payload", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello report"), 0o644)).To(Succeed())
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayEditTool{Bridge: bridge, Store: fakeMemStore{root: dir}}
		Expect(tool.Call(context.Background(), `{"id":"C1/1","text":"done","attachments":[{"slot":"scratchpad","path":"report.txt"}]}`)).To(Equal("Message C1/1 updated."))
		Expect(bridge.payload).To(MatchJSON(`{"text":"done"}`))
		Expect(bridge.attachments).To(HaveLen(1))
		Expect(bridge.attachments[0].Filename).To(Equal("report.txt"))
		Expect(string(bridge.attachments[0].Content)).To(Equal("hello report"))
	})

	It("errors when attachments are requested but no store is available", func() {
		bridge := &fakeGatewayBridge{}
		tool := &aitools.GatewayEditTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"id":"C1/1","text":"done","attachments":[{"slot":"memory","path":"x.txt"}]}`)).To(ContainSubstring("not available"))
		Expect(bridge.editedID).To(BeEmpty())
	})

	It("advertises attachments only when a memory store is available", func() {
		bridge := &fakeGatewayBridge{}
		without := &aitools.GatewayEditTool{Bridge: bridge}
		Expect(string(without.ToolPayloadSchema().ToJSONSchema())).NotTo(ContainSubstring("attachments"))
		with := &aitools.GatewayEditTool{Bridge: bridge, Store: fakeMemStore{root: "/tmp"}}
		Expect(string(with.ToolPayloadSchema().ToJSONSchema())).To(ContainSubstring("attachments"))
		Expect(with.ToolDescription()).To(ContainSubstring("attachments"))
	})

	It("surfaces a gateway error to the agent", func() {
		bridge := &fakeGatewayBridge{err: errors.New("message C1/1 is a question")}
		tool := &aitools.GatewayEditTool{Bridge: bridge}
		Expect(tool.Call(context.Background(), `{"id":"C1/1","text":"done"}`)).To(ContainSubstring("is a question"))
	})

	It("advertises the gateway's post schema with a required id", func() {
		bridge := &fakeGatewayBridge{schema: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`}
		tool := &aitools.GatewayEditTool{Bridge: bridge}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		Expect(json.Unmarshal(tool.ToolPayloadSchema().ToJSONSchema(), &schema)).To(Succeed())
		Expect(schema.Properties).To(HaveKey("text"))
		Expect(schema.Properties).To(HaveKey("id"))
		Expect(schema.Required).To(ConsistOf("text", "id"))
	})
})
