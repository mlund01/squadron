package prompts_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/agent/internal/prompts"
	"squadron/aitools"
)

func TestPrompts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Prompts Suite")
}

// fakeStore is a minimal MemoryStore for prompt-rendering tests. Only
// MemoryInfos is exercised.
type fakeStore struct {
	infos []aitools.MemoryInfo
}

func (s *fakeStore) ResolvePath(string, string) (string, error) { return "", nil }
func (s *fakeStore) MemoryInfos() []aitools.MemoryInfo          { return s.infos }

var _ = Describe("FormatMemoryContext", func() {
	It("returns empty string for nil store", func() {
		Expect(prompts.FormatMemoryContext(nil)).To(Equal(""))
	})

	It("returns empty string when the store has no slots", func() {
		Expect(prompts.FormatMemoryContext(&fakeStore{})).To(Equal(""))
	})

	It("labels the persistent mission memory slot specially", func() {
		got := prompts.FormatMemoryContext(&fakeStore{infos: []aitools.MemoryInfo{
			{Name: aitools.MemorySlotName, Description: "running notes"},
		}})
		Expect(got).To(ContainSubstring("persistent mission memory"))
		Expect(got).To(ContainSubstring("running notes"))
		Expect(got).To(ContainSubstring("**memory**"))
	})

	It("labels the scratchpad slot specially", func() {
		got := prompts.FormatMemoryContext(&fakeStore{infos: []aitools.MemoryInfo{
			{Name: aitools.ScratchpadSlotName},
		}})
		Expect(got).To(ContainSubstring("ephemeral per-run scratchpad"))
	})

	It("does not add a reserved-name label for shared slots", func() {
		got := prompts.FormatMemoryContext(&fakeStore{infos: []aitools.MemoryInfo{
			{Name: "research", Description: "shared notes"},
		}})
		Expect(got).NotTo(ContainSubstring("persistent mission memory"))
		Expect(got).NotTo(ContainSubstring("ephemeral per-run"))
		Expect(got).To(ContainSubstring("**research**"))
		Expect(got).To(ContainSubstring("shared notes"))
	})

	It("teaches the agent the `slot` param + all six file_* tool names", func() {
		got := prompts.FormatMemoryContext(&fakeStore{infos: []aitools.MemoryInfo{
			{Name: aitools.MemorySlotName, Description: "x"},
		}})
		Expect(got).To(ContainSubstring("`slot` parameter"))
		for _, tool := range []string{"file_list", "file_read", "file_create", "file_delete", "file_search", "file_grep"} {
			Expect(got).To(ContainSubstring(tool))
		}
	})

	It("preserves store iteration order (sort is the store's responsibility, not the prompt's)", func() {
		got := prompts.FormatMemoryContext(&fakeStore{infos: []aitools.MemoryInfo{
			{Name: "zeta", Description: "z"},
			{Name: "alpha", Description: "a"},
		}})
		zIdx := strings.Index(got, "**zeta**")
		aIdx := strings.Index(got, "**alpha**")
		Expect(zIdx).To(BeNumerically(">=", 0))
		Expect(aIdx).To(BeNumerically(">=", 0))
		Expect(zIdx).To(BeNumerically("<", aIdx), "expected store order preserved (zeta first)")
	})
})
