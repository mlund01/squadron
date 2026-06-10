package aitools_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

// fakeStore is a minimal MemoryStore for tool tests. Each slot is backed by
// a real on-disk directory so the tools exercise true filesystem syscalls.
type fakeStore struct {
	root  string
	slots map[string]string // slot name → absolute path
}

func newFakeStore(slots ...string) *fakeStore {
	root, err := os.MkdirTemp("", "memorytools-")
	Expect(err).NotTo(HaveOccurred())
	s := &fakeStore{root: root, slots: map[string]string{}}
	for _, name := range slots {
		dir := filepath.Join(root, name)
		Expect(os.MkdirAll(dir, 0755)).To(Succeed())
		s.slots[name] = dir
	}
	return s
}

func (s *fakeStore) cleanup() { _ = os.RemoveAll(s.root) }

func (s *fakeStore) ResolvePath(slot string, relPath string) (string, error) {
	if slot == "" {
		return "", fmt.Errorf("slot name is required")
	}
	abs, ok := s.slots[slot]
	if !ok {
		return "", fmt.Errorf("slot %q not found", slot)
	}
	cleaned := filepath.Clean(relPath)
	if cleaned == "." {
		return abs, nil
	}
	full := filepath.Join(abs, cleaned)
	if !strings.HasPrefix(full, abs+string(filepath.Separator)) && full != abs {
		return "", fmt.Errorf("path escapes slot root")
	}
	return full, nil
}

func (s *fakeStore) MemoryInfos() []aitools.MemoryInfo {
	infos := make([]aitools.MemoryInfo, 0, len(s.slots))
	for name := range s.slots {
		infos = append(infos, aitools.MemoryInfo{Name: name})
	}
	return infos
}

var _ = Describe("Memory file tools", func() {
	var (
		store *fakeStore
		ctx   = context.Background()
	)

	BeforeEach(func() { store = newFakeStore("memory") })
	AfterEach(func() { store.cleanup() })

	Describe("schema", func() {
		It("advertises `slot` (not legacy `folder`) on every tool", func() {
			tools := []aitools.Tool{
				&aitools.MemoryListTool{Store: store},
				&aitools.MemoryReadTool{Store: store},
				&aitools.MemoryCreateTool{Store: store},
				&aitools.MemoryDeleteTool{Store: store},
				&aitools.MemorySearchTool{Store: store},
				&aitools.MemoryGrepTool{Store: store},
			}
			for _, tl := range tools {
				schema := tl.ToolPayloadSchema()
				Expect(schema.Properties).To(HaveKey("slot"), "%s missing `slot`", tl.ToolName())
				Expect(schema.Properties).NotTo(HaveKey("folder"), "%s still advertises legacy `folder`", tl.ToolName())
				Expect(schema.Required).To(ContainElement("slot"), "%s Required missing `slot`", tl.ToolName())
				Expect(schema.Required).NotTo(ContainElement("folder"), "%s Required still has `folder`", tl.ToolName())
			}
		})

		It("rejects legacy {\"folder\": ...} JSON payloads", func() {
			out := (&aitools.MemoryListTool{Store: store}).Call(ctx, `{"folder": "memory"}`)
			Expect(out).To(ContainSubstring("Error"))
		})
	})

	Describe("file_list", func() {
		It("lists files and directories with their types and sizes", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "a.txt"), []byte("x"), 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "b.txt"), []byte("yy"), 0644)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(store.slots["memory"], "sub"), 0755)).To(Succeed())

			out := (&aitools.MemoryListTool{Store: store}).Call(ctx, `{"slot": "memory"}`)
			Expect(out).To(ContainSubstring("a.txt"))
			Expect(out).To(ContainSubstring("b.txt"))
			Expect(out).To(ContainSubstring("sub/"))
		})

		It("recurses into subdirectories when recursive=true", func() {
			Expect(os.MkdirAll(filepath.Join(store.slots["memory"], "sub"), 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "sub", "deep.txt"), []byte("z"), 0644)).To(Succeed())

			out := (&aitools.MemoryListTool{Store: store}).Call(ctx, `{"slot": "memory", "recursive": true}`)
			Expect(out).To(ContainSubstring("sub/deep.txt"))
		})

		It("paginates via limit + offset", func() {
			for i := 0; i < 5; i++ {
				Expect(os.WriteFile(filepath.Join(store.slots["memory"], fmt.Sprintf("f%d", i)), []byte("x"), 0644)).To(Succeed())
			}
			out := (&aitools.MemoryListTool{Store: store}).Call(ctx, `{"slot": "memory", "limit": 2, "offset": 0}`)
			Expect(out).To(ContainSubstring("Showing 1-2 of 5"))
			Expect(out).To(ContainSubstring("use offset: 2 to continue"))
		})

		It("reports empty directories explicitly", func() {
			out := (&aitools.MemoryListTool{Store: store}).Call(ctx, `{"slot": "memory"}`)
			Expect(out).To(ContainSubstring("(empty directory)"))
		})

		It("errors on unknown slot", func() {
			out := (&aitools.MemoryListTool{Store: store}).Call(ctx, `{"slot": "doesnotexist"}`)
			Expect(out).To(HavePrefix("Error"))
		})
	})

	Describe("file_read", func() {
		It("returns file contents verbatim", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("hello\nworld\n"), 0644)).To(Succeed())
			out := (&aitools.MemoryReadTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt"}`)
			Expect(out).To(Equal("hello\nworld\n"))
		})

		It("truncates to the first N lines via max_lines", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("one\ntwo\nthree\nfour\n"), 0644)).To(Succeed())
			out := (&aitools.MemoryReadTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt", "max_lines": 2}`)
			Expect(out).To(Equal("one\ntwo"))
		})

		It("truncates to the first N bytes via max_bytes", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("abcdefghij"), 0644)).To(Succeed())
			out := (&aitools.MemoryReadTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt", "max_bytes": 5}`)
			Expect(out).To(Equal("abcde"))
		})

		It("errors when path is missing", func() {
			out := (&aitools.MemoryReadTool{Store: store}).Call(ctx, `{"slot": "memory"}`)
			Expect(out).To(ContainSubstring("path is required"))
		})

		It("rejects reading a directory as a file", func() {
			Expect(os.MkdirAll(filepath.Join(store.slots["memory"], "sub"), 0755)).To(Succeed())
			out := (&aitools.MemoryReadTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "sub"}`)
			Expect(out).To(ContainSubstring("directory"))
		})

		It("rejects path traversal", func() {
			out := (&aitools.MemoryReadTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "../escape"}`)
			Expect(out).To(ContainSubstring("invalid path"))
		})
	})

	Describe("file_create", func() {
		It("creates a new file when none exists", func() {
			out := (&aitools.MemoryCreateTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt", "content": "hi"}`)
			Expect(out).To(ContainSubstring("created successfully"))
			b, err := os.ReadFile(filepath.Join(store.slots["memory"], "f.txt"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal("hi"))
		})

		It("refuses to overwrite an existing file by default", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("old"), 0644)).To(Succeed())
			out := (&aitools.MemoryCreateTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt", "content": "new"}`)
			Expect(out).To(ContainSubstring("already exists"))
			b, _ := os.ReadFile(filepath.Join(store.slots["memory"], "f.txt"))
			Expect(string(b)).To(Equal("old"))
		})

		It("overwrites when overwrite=true", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("old"), 0644)).To(Succeed())
			out := (&aitools.MemoryCreateTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt", "content": "new", "overwrite": true}`)
			Expect(out).To(ContainSubstring("created successfully"))
			b, _ := os.ReadFile(filepath.Join(store.slots["memory"], "f.txt"))
			Expect(string(b)).To(Equal("new"))
		})

		It("appends when append=true and the target exists", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("a"), 0644)).To(Succeed())
			out := (&aitools.MemoryCreateTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt", "content": "b", "append": true}`)
			Expect(out).To(ContainSubstring("appended"))
			b, _ := os.ReadFile(filepath.Join(store.slots["memory"], "f.txt"))
			Expect(string(b)).To(Equal("ab"))
		})

		It("errors when append=true and the target is missing", func() {
			out := (&aitools.MemoryCreateTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "missing.txt", "content": "x", "append": true}`)
			Expect(out).To(ContainSubstring("Error"))
		})

		It("creates parent directories as needed", func() {
			out := (&aitools.MemoryCreateTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "a/b/c.txt", "content": "x"}`)
			Expect(out).To(ContainSubstring("created successfully"))
			_, err := os.Stat(filepath.Join(store.slots["memory"], "a", "b", "c.txt"))
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("file_delete", func() {
		It("removes a file", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("x"), 0644)).To(Succeed())
			out := (&aitools.MemoryDeleteTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "f.txt"}`)
			Expect(out).To(ContainSubstring("deleted successfully"))
			_, err := os.Stat(filepath.Join(store.slots["memory"], "f.txt"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("refuses to delete directories", func() {
			Expect(os.MkdirAll(filepath.Join(store.slots["memory"], "sub"), 0755)).To(Succeed())
			out := (&aitools.MemoryDeleteTool{Store: store}).Call(ctx, `{"slot": "memory", "path": "sub"}`)
			Expect(out).To(ContainSubstring("cannot delete directories"))
			_, err := os.Stat(filepath.Join(store.slots["memory"], "sub"))
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("file_search", func() {
		It("returns files matching the regex by filename", func() {
			for _, f := range []string{"alpha.go", "beta.go", "notes.txt"} {
				Expect(os.WriteFile(filepath.Join(store.slots["memory"], f), []byte("x"), 0644)).To(Succeed())
			}
			out := (&aitools.MemorySearchTool{Store: store}).Call(ctx, `{"slot": "memory", "pattern": "\\.go$"}`)
			Expect(out).To(ContainSubstring("alpha.go"))
			Expect(out).To(ContainSubstring("beta.go"))
			Expect(out).NotTo(ContainSubstring("notes.txt"))
		})

		It("reports a no-match summary", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"), []byte("x"), 0644)).To(Succeed())
			out := (&aitools.MemorySearchTool{Store: store}).Call(ctx, `{"slot": "memory", "pattern": "nope"}`)
			Expect(out).To(ContainSubstring("No files found"))
		})

		It("errors on an invalid regex", func() {
			out := (&aitools.MemorySearchTool{Store: store}).Call(ctx, `{"slot": "memory", "pattern": "([invalid"}`)
			Expect(out).To(ContainSubstring("invalid regex"))
		})
	})

	Describe("file_grep", func() {
		It("returns matching lines with paths and line numbers (flat)", func() {
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "f.txt"),
				[]byte("first line\nTODO: fixme\nthird line\n"), 0644)).To(Succeed())
			out := (&aitools.MemoryGrepTool{Store: store}).Call(ctx, `{"slot": "memory", "pattern": "TODO"}`)
			Expect(out).To(ContainSubstring("f.txt:2"))
			Expect(out).To(ContainSubstring("TODO: fixme"))
		})

		It("descends into subdirs when recursive=true", func() {
			Expect(os.MkdirAll(filepath.Join(store.slots["memory"], "sub"), 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "sub", "deep.txt"),
				[]byte("hello\nTODO inside\n"), 0644)).To(Succeed())
			out := (&aitools.MemoryGrepTool{Store: store}).Call(ctx, `{"slot": "memory", "pattern": "TODO", "recursive": true}`)
			Expect(out).To(ContainSubstring("sub/deep.txt:2"))
		})

		It("does NOT descend into subdirs by default", func() {
			Expect(os.MkdirAll(filepath.Join(store.slots["memory"], "sub"), 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(store.slots["memory"], "sub", "deep.txt"), []byte("TODO\n"), 0644)).To(Succeed())
			out := (&aitools.MemoryGrepTool{Store: store}).Call(ctx, `{"slot": "memory", "pattern": "TODO"}`)
			Expect(out).To(ContainSubstring("No matches"))
		})
	})
})
