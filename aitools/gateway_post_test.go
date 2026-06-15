package aitools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

type fakeGatewayBridge struct {
	payload     string
	attachments []aitools.GatewayAttachment
	err         error
	desc        string
	schema      string
}

func (f *fakeGatewayBridge) PostMessage(_ context.Context, payload string, attachments []aitools.GatewayAttachment) error {
	f.payload = payload
	f.attachments = attachments
	return f.err
}
func (f *fakeGatewayBridge) MessageToolDescription() string { return f.desc }
func (f *fakeGatewayBridge) MessageToolSchema() string      { return f.schema }

// fakeMemStore resolves any slot to a fixed root dir for attachment tests.
type fakeMemStore struct{ root string }

func (s fakeMemStore) ResolvePath(_ string, relPath string) (string, error) {
	return filepath.Join(s.root, relPath), nil
}
func (s fakeMemStore) MemoryInfos() []aitools.MemoryInfo { return nil }

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

	It("advertises attachments only when a memory store is wired", func() {
		gw := &fakeGatewayBridge{schema: `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`}
		without := &aitools.GatewayPostTool{Bridge: gw}
		Expect(string(without.ToolPayloadSchema().ToJSONSchema())).NotTo(ContainSubstring("attachments"))
		Expect(without.ToolDescription()).NotTo(ContainSubstring("attachments"))

		with := &aitools.GatewayPostTool{Bridge: gw, Store: fakeMemStore{root: "/tmp"}}
		Expect(string(with.ToolPayloadSchema().ToJSONSchema())).To(ContainSubstring("attachments"))
		Expect(with.ToolDescription()).To(ContainSubstring("attachments"))
	})

	It("resolves local-file attachments to bytes and strips them from the payload", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello report"), 0o644)).To(Succeed())

		b := &fakeGatewayBridge{}
		t := &aitools.GatewayPostTool{Bridge: b, Store: fakeMemStore{root: dir}}
		out := t.Call(context.Background(),
			`{"text":"see attached","attachments":[{"slot":"scratchpad","path":"report.txt"}]}`)

		Expect(out).To(ContainSubstring("posted"))
		Expect(b.attachments).To(HaveLen(1))
		Expect(b.attachments[0].Filename).To(Equal("report.txt"))
		Expect(string(b.attachments[0].Content)).To(Equal("hello report"))
		// the gateway must not see the attachments reference, only text
		Expect(b.payload).To(ContainSubstring(`"text":"see attached"`))
		Expect(b.payload).NotTo(ContainSubstring("attachments"))
	})

	It("errors when attachments are requested but no store is available", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{}}
		out := t.Call(context.Background(), `{"text":"hi","attachments":[{"slot":"memory","path":"x.txt"}]}`)
		Expect(out).To(ContainSubstring("not available"))
	})

	It("errors when an attachment file does not exist", func() {
		t := &aitools.GatewayPostTool{Bridge: &fakeGatewayBridge{}, Store: fakeMemStore{root: GinkgoT().TempDir()}}
		out := t.Call(context.Background(), `{"text":"hi","attachments":[{"slot":"memory","path":"missing.txt"}]}`)
		Expect(out).To(ContainSubstring("Error:"))
	})
})
