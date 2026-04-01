package aitools_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

func TestImageDetector(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ImageDetector Suite")
}

var _ = Describe("ExtractImages", func() {
	Describe("empty and non-image input", func() {
		It("returns empty for empty string", func() {
			r := aitools.ExtractImages("")
			Expect(r.Images).To(BeEmpty())
			Expect(r.RemainingText).To(BeEmpty())
		})

		It("returns empty for plain text", func() {
			r := aitools.ExtractImages("hello world")
			Expect(r.Images).To(BeEmpty())
			Expect(r.RemainingText).To(Equal("hello world"))
		})

		It("returns empty for JSON without images", func() {
			r := aitools.ExtractImages(`{"key": "value", "count": 42}`)
			Expect(r.Images).To(BeEmpty())
			Expect(r.RemainingText).To(Equal(`{"key": "value", "count": 42}`))
		})
	})

	Describe("data URL extraction", func() {
		It("extracts a single PNG data URL", func() {
			input := `data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/png"))
			Expect(r.Images[0].Data).To(Equal("iVBORw0KGgoAAAANSUhEUg=="))
			Expect(r.RemainingText).To(Equal("[image]"))
		})

		It("extracts a JPEG data URL", func() {
			input := `data:image/jpeg;base64,/9j/4AAQSkZJRg==`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/jpeg"))
		})

		It("normalizes jpg to jpeg", func() {
			input := `data:image/jpg;base64,/9j/4AAQSkZJRg==`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/jpeg"))
		})

		It("extracts a GIF data URL", func() {
			input := `data:image/gif;base64,R0lGODlhAQABAIAAAP==`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/gif"))
		})

		It("extracts a WebP data URL", func() {
			input := `data:image/webp;base64,UklGRlYAAABXRUJQ`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/webp"))
		})

		It("extracts multiple data URLs from JSON", func() {
			input := `{"img1": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==", "img2": "data:image/jpeg;base64,/9j/4AAQSkZJRg=="}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(2))
			Expect(r.Images[0].MediaType).To(Equal("image/png"))
			Expect(r.Images[1].MediaType).To(Equal("image/jpeg"))
			Expect(r.RemainingText).To(Equal(`{"img1": "[image]", "img2": "[image]"}`))
		})

		It("preserves surrounding text", func() {
			input := `prefix data:image/png;base64,iVBORw0KGgoAAAANSUhEUg== suffix`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.RemainingText).To(Equal("prefix [image] suffix"))
		})
	})

	Describe("JSON embedded base64 extraction", func() {
		It("extracts PNG from JSON value", func() {
			b64 := "iVBORw0KGgo" + strings.Repeat("AAAA", 30) // >100 chars
			input := `{"screenshot": "` + b64 + `"}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/png"))
			Expect(r.Images[0].Data).To(Equal(b64))
			Expect(r.RemainingText).To(ContainSubstring("[image]"))
		})

		It("extracts JPEG from JSON value", func() {
			b64 := "/9j/" + strings.Repeat("AAAA", 30)
			input := `{"photo": "` + b64 + `"}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/jpeg"))
		})

		It("ignores short base64 strings in JSON", func() {
			input := `{"short": "iVBORw0KGgoShort"}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(BeEmpty())
		})

		It("ignores non-image base64 in JSON", func() {
			input := `{"data": "` + strings.Repeat("AAAA", 30) + `"}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(BeEmpty())
		})
	})

	Describe("raw base64 detection", func() {
		It("detects raw PNG base64", func() {
			b64 := "iVBORw0KGgo" + strings.Repeat("AAAA", 10)
			r := aitools.ExtractImages(b64)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/png"))
			Expect(r.RemainingText).To(Equal("[image]"))
		})

		It("detects raw JPEG base64", func() {
			b64 := "/9j/" + strings.Repeat("BBBB", 10)
			r := aitools.ExtractImages(b64)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/jpeg"))
		})

		It("detects raw GIF base64", func() {
			b64 := "R0lGOD" + strings.Repeat("CCCC", 10)
			r := aitools.ExtractImages(b64)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/gif"))
		})

		It("detects raw WebP base64", func() {
			b64 := "UklGR" + strings.Repeat("DDDD", 10)
			r := aitools.ExtractImages(b64)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.Images[0].MediaType).To(Equal("image/webp"))
		})

		It("does not match raw base64 if string has non-base64 chars", func() {
			r := aitools.ExtractImages("iVBORw0KGgo has spaces in it")
			Expect(r.Images).To(BeEmpty())
		})

		It("does not match very short base64", func() {
			r := aitools.ExtractImages("iVBORw0KGgo")
			Expect(r.Images).To(BeEmpty())
		})

		It("does not fire raw detection when data URLs already extracted", func() {
			// The entire string is a data URL — raw detection should not re-match
			input := `data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
		})
	})

	Describe("mixed content", func() {
		It("handles JSON with data URL images and text fields", func() {
			input := `{"question": "What color?", "image": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(1))
			Expect(r.RemainingText).To(ContainSubstring(`"question": "What color?"`))
			Expect(r.RemainingText).To(ContainSubstring("[image]"))
			Expect(r.RemainingText).NotTo(ContainSubstring("iVBOR"))
		})

		It("handles multiple images in an array", func() {
			input := `{"images": ["data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==", "data:image/jpeg;base64,/9j/4AAQSkZJRg=="]}`
			r := aitools.ExtractImages(input)
			Expect(r.Images).To(HaveLen(2))
			Expect(r.Images[0].MediaType).To(Equal("image/png"))
			Expect(r.Images[1].MediaType).To(Equal("image/jpeg"))
		})
	})

	Describe("DetectImage (single image compat)", func() {
		It("returns first image for data URL", func() {
			img := aitools.DetectImage(`data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==`)
			Expect(img).NotTo(BeNil())
			Expect(img.MediaType).To(Equal("image/png"))
		})

		It("returns nil for plain text", func() {
			img := aitools.DetectImage("just text")
			Expect(img).To(BeNil())
		})

		It("returns nil for empty string", func() {
			img := aitools.DetectImage("")
			Expect(img).To(BeNil())
		})
	})
})
