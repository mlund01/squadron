package aitools_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

func writeSlotFile(store *fakeStore, slot, name string, data []byte) {
	abs, err := store.ResolvePath(slot, name)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(abs, data, 0644)).To(Succeed())
}

func viewParams(slot, path string) string {
	return `{"slot":"` + slot + `","path":"` + path + `"}`
}

var _ = Describe("file_view tool", func() {
	var (
		store *fakeStore
		tool  *aitools.FileViewTool
		ctx   = context.Background()
	)

	BeforeEach(func() {
		store = newFakeStore("input.doc", "memory", "packet.kb", "scratchpad")
		tool = &aitools.FileViewTool{Store: store}
	})
	AfterEach(func() { store.cleanup() })

	It("returns an image block for a PNG", func() {
		writeSlotFile(store, "input.doc", "shot.png", append(pngSignature, []byte("rest")...))
		text, media := tool.CallMedia(ctx, viewParams("input.doc", "shot.png"))
		Expect(media).To(HaveLen(1))
		Expect(media[0].Kind).To(Equal(aitools.MediaKindImage))
		Expect(media[0].MediaType).To(Equal("image/png"))
		Expect(text).To(ContainSubstring("image"))
	})

	It("returns a document block for a PDF", func() {
		writeSlotFile(store, "input.doc", "report.pdf", []byte("%PDF-1.7\n1 0 obj\n"))
		_, media := tool.CallMedia(ctx, viewParams("input.doc", "report.pdf"))
		Expect(media).To(HaveLen(1))
		Expect(media[0].Kind).To(Equal(aitools.MediaKindDocument))
		Expect(media[0].MediaType).To(Equal("application/pdf"))
	})

	It("rasterizes an SVG to a PNG image block", func() {
		svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="red"/></svg>`)
		writeSlotFile(store, "packet.kb", "icon.svg", svg)
		text, media := tool.CallMedia(ctx, viewParams("packet.kb", "icon.svg"))
		Expect(media).To(HaveLen(1))
		Expect(media[0].Kind).To(Equal(aitools.MediaKindImage))
		Expect(media[0].MediaType).To(Equal("image/png"))
		decoded, err := base64.StdEncoding.DecodeString(media[0].Data)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded[:8]).To(Equal(pngSignature))
		Expect(text).To(ContainSubstring("Rasterized"))
	})

	It("falls back to a text pointer for a non-viewable file", func() {
		writeSlotFile(store, "memory", "notes.txt", []byte("just some prose"))
		text, media := tool.CallMedia(ctx, viewParams("memory", "notes.txt"))
		Expect(media).To(BeEmpty())
		Expect(text).To(ContainSubstring("file_read"))
	})

	It("detects by magic bytes even when the extension lies", func() {
		writeSlotFile(store, "scratchpad", "actually_png.txt", append(pngSignature, []byte("x")...))
		_, media := tool.CallMedia(ctx, viewParams("scratchpad", "actually_png.txt"))
		Expect(media).To(HaveLen(1))
		Expect(media[0].Kind).To(Equal(aitools.MediaKindImage))
	})

	It("surfaces a text fallback when an SVG cannot be parsed", func() {
		writeSlotFile(store, "memory", "broken.svg", []byte("<svg \x00 not valid xml at all"))
		_, media := tool.CallMedia(ctx, viewParams("memory", "broken.svg"))
		Expect(media).To(BeEmpty())
	})

	It("implements the MediaTool interface", func() {
		_, ok := aitools.MediaToolOf(tool)
		Expect(ok).To(BeTrue())
	})
})

var _ = Describe("file_read binary rejection", func() {
	var store *fakeStore
	ctx := context.Background()

	BeforeEach(func() { store = newFakeStore("memory") })
	AfterEach(func() { store.cleanup() })

	It("rejects binary content and points to file_view, even on a writable slot", func() {
		abs := filepath.Join(store.slots["memory"], "blob.bin")
		Expect(os.WriteFile(abs, append(pngSignature, 0x00, 0x01), 0644)).To(Succeed())
		read := &aitools.MemoryReadTool{Store: store}
		out := read.Call(ctx, viewParams("memory", "blob.bin"))
		Expect(out).To(ContainSubstring("file_view"))
	})
})
