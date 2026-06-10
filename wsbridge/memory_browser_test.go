package wsbridge

import (
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/config"
	"squadron/internal/paths"
)

// withTempHome installs a fresh SquadronHome so mission.SharedMemoryPath /
// MissionMemoryPath return predictable paths under a per-test temp dir.
// Must be called inside a spec; the cleanup is registered via DeferCleanup.
func withTempHome() string {
	home, err := os.MkdirTemp("", "wsbridge-home-")
	Expect(err).NotTo(HaveOccurred())
	paths.ResetHome()
	Expect(paths.SetHome(home)).To(Succeed())
	DeferCleanup(func() {
		paths.ResetHome()
		_ = os.RemoveAll(home)
	})
	return home
}

func clientWithConfig(cfg *config.Config) *Client {
	return &Client{cfg: cfg, cfgMu: sync.RWMutex{}}
}

var _ = Describe("wsbridge memory browser", func() {

	Describe("collectMemoryInfos", func() {
		It("emits one entry per shared memory and one per per-mission memory", func() {
			home := withTempHome()
			cfg := &config.Config{
				Memories: []config.Memory{
					{Name: "research", Description: "Research notes"},
				},
				Missions: []config.Mission{
					{
						Name:     "analyze",
						Memory:   &config.MissionMemory{Description: "Mission's persistent state"},
						Memories: []string{"research"},
					},
				},
			}
			infos, err := collectMemoryInfos(cfg)
			Expect(err).NotTo(HaveOccurred())
			Expect(infos).To(HaveLen(2))

			byName := map[string]int{}
			for i, inf := range infos {
				byName[inf.Name] = i
			}
			Expect(byName).To(HaveKey("research"))
			Expect(byName).To(HaveKey("analyze"))

			research := infos[byName["research"]]
			Expect(research.IsShared).To(BeTrue())
			Expect(research.Path).To(Equal(filepath.Join(home, "memories", "shared", "research")))
			Expect(research.Missions).To(ContainElement("analyze"))

			missionMem := infos[byName["analyze"]]
			Expect(missionMem.IsShared).To(BeFalse())
			Expect(missionMem.Path).To(Equal(filepath.Join(home, "memories", "mission", "analyze")))
		})

		It("marks every memory as editable (no read-only mode anymore)", func() {
			withTempHome()
			cfg := &config.Config{
				Memories: []config.Memory{{Name: "shared", Description: "x"}},
				Missions: []config.Mission{{Name: "m", Memory: &config.MissionMemory{Description: "x"}}},
			}
			infos, err := collectMemoryInfos(cfg)
			Expect(err).NotTo(HaveOccurred())
			for _, inf := range infos {
				Expect(inf.Editable).To(BeTrue(), "%s should be editable", inf.Name)
			}
		})

		It("skips missions with no `memory { }` block and does not expose scratchpads", func() {
			withTempHome()
			cfg := &config.Config{
				Missions: []config.Mission{
					{Name: "no_memory_mission"},
					{Name: "scratchpad_only_mission", Scratchpad: true},
				},
			}
			infos, err := collectMemoryInfos(cfg)
			Expect(err).NotTo(HaveOccurred())
			Expect(infos).To(BeEmpty())
		})
	})

	Describe("resolveMemoryPath", func() {
		It("resolves a shared memory by label under SquadronHome", func() {
			home := withTempHome()
			c := clientWithConfig(&config.Config{
				Memories: []config.Memory{{Name: "notes", Description: "x"}},
			})

			rm, full, err := c.resolveMemoryPath("notes", "subdir/file.txt")
			Expect(err).NotTo(HaveOccurred())
			Expect(rm.name).To(Equal("notes"))
			Expect(full).To(Equal(filepath.Join(home, "memories", "shared", "notes", "subdir", "file.txt")))
		})

		It("resolves a mission's persistent memory by mission name", func() {
			home := withTempHome()
			c := clientWithConfig(&config.Config{
				Missions: []config.Mission{{
					Name:   "analyze",
					Memory: &config.MissionMemory{Description: "x"},
				}},
			})

			rm, full, err := c.resolveMemoryPath("analyze", "report.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(rm.name).To(Equal("analyze"))
			Expect(full).To(Equal(filepath.Join(home, "memories", "mission", "analyze", "report.md")))
		})

		It("gives shared memory priority over a mission with the same name (collision is blocked at config-load; this pins runtime behavior)", func() {
			home := withTempHome()
			c := clientWithConfig(&config.Config{
				Memories: []config.Memory{{Name: "twins", Description: "shared"}},
				Missions: []config.Mission{{
					Name:   "twins",
					Memory: &config.MissionMemory{Description: "mission"},
				}},
			})

			_, full, err := c.resolveMemoryPath("twins", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(full).To(Equal(filepath.Join(home, "memories", "shared", "twins")))
		})

		It("errors on unknown name", func() {
			withTempHome()
			c := clientWithConfig(&config.Config{})
			_, _, err := c.resolveMemoryPath("does_not_exist", "")
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})

		It("does not resolve a mission that has no `memory { }` block", func() {
			withTempHome()
			c := clientWithConfig(&config.Config{
				Missions: []config.Mission{{Name: "no_memory_mission"}},
			})
			_, _, err := c.resolveMemoryPath("no_memory_mission", "")
			Expect(err).To(HaveOccurred())
		})

		It("reads a real file end-to-end via the resolved path", func() {
			home := withTempHome()
			sharedDir := filepath.Join(home, "memories", "shared", "notes")
			Expect(os.MkdirAll(sharedDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(sharedDir, "hello.txt"), []byte("hi"), 0644)).To(Succeed())

			c := clientWithConfig(&config.Config{
				Memories: []config.Memory{{Name: "notes", Description: "x"}},
			})
			_, full, err := c.resolveMemoryPath("notes", "hello.txt")
			Expect(err).NotTo(HaveOccurred())
			b, err := os.ReadFile(full)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal("hi"))
		})
	})

	Describe("resolveSafePath", func() {
		var (
			c    *Client
			base string
		)

		BeforeEach(func() {
			c = &Client{}
			var err error
			base, err = os.MkdirTemp("", "safepath-")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(base) })
		})

		It("rejects a leading `..`", func() {
			_, err := c.resolveSafePath(base, "../escape")
			Expect(err).To(HaveOccurred())
		})

		It("rejects a mid-path `..`", func() {
			_, err := c.resolveSafePath(base, "sub/../../escape")
			Expect(err).To(HaveOccurred())
		})

		It("rejects absolute paths", func() {
			_, err := c.resolveSafePath(base, "/etc/passwd")
			Expect(err).To(HaveOccurred())
		})

		It("accepts a valid relative subpath", func() {
			got, err := c.resolveSafePath(base, "sub/file.txt")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(filepath.Join(base, "sub", "file.txt")))
		})

		It("returns the absolute root when rel is empty", func() {
			got, err := c.resolveSafePath(base, "")
			Expect(err).NotTo(HaveOccurred())
			absBase, _ := filepath.Abs(base)
			Expect(got).To(Equal(absBase))
		})
	})
})
