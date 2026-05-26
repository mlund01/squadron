package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Memory + Scratchpad", func() {

	Describe("top-level memory block", func() {
		It("parses a memory block and exposes it via memories.NAME", func() {
			hcl := fullBaseHCL() + `
memory "research" {
  description = "Research docs"
  label       = "Research"
  editable    = true
}
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memories  = [memories.research]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Memories).To(HaveLen(1))
			Expect(cfg.Memories[0].Name).To(Equal("research"))
			Expect(cfg.Memories[0].Editable).To(BeTrue())
			Expect(cfg.Missions[0].Memories).To(ConsistOf("research"))
		})

		It("rejects the reserved name 'memory'", func() {
			m := config.Memory{Name: "memory"}
			err := m.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects the reserved name 'scratchpad'", func() {
			m := config.Memory{Name: "scratchpad"}
			err := m.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects the old shared_folder block with a pointer at the new syntax", func() {
			hcl := fullBaseHCL() + `
shared_folder "research" {
  path = "./data"
}
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer supported"))
			Expect(err.Error()).To(ContainSubstring("memory \"research\""))
		})

		It("rejects the old `path` attribute on a memory block", func() {
			hcl := fullBaseHCL() + `
memory "research" {
  path = "./data"
}
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("mission memory block (persistent)", func() {
		It("parses a memory block on a mission", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory {
    description = "Long-term notes"
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Memory).NotTo(BeNil())
			Expect(cfg.Missions[0].Memory.Description).To(Equal("Long-term notes"))
			Expect(cfg.Missions[0].Scratchpad).To(BeNil())
		})

		It("rejects two memory blocks on the same mission", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { description = "a" }
  memory { description = "b" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one memory block allowed"))
		})

		It("rejects a `type` attribute (was removed when the block split)", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { type = "persistent" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})

		It("rejects a `path` attribute on a mission memory block", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { path = "./x" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("mission scratchpad block (ephemeral)", func() {
		It("parses a scratchpad block with default cleanup", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  scratchpad {
    description = "Scratch"
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Scratchpad).NotTo(BeNil())
			Expect(cfg.Missions[0].Scratchpad.Description).To(Equal("Scratch"))
			Expect(cfg.Missions[0].Scratchpad.Cleanup).NotTo(BeNil())
			Expect(*cfg.Missions[0].Scratchpad.Cleanup).To(Equal(config.DefaultScratchpadCleanupDays))
			Expect(cfg.Missions[0].Memory).To(BeNil())
		})

		It("preserves an explicit cleanup = 0 (keep forever)", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  scratchpad { cleanup = 0 }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Scratchpad.Cleanup).NotTo(BeNil())
			Expect(*cfg.Missions[0].Scratchpad.Cleanup).To(Equal(0))
		})

		It("rejects two scratchpad blocks", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  scratchpad {}
  scratchpad {}
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one scratchpad block allowed"))
		})

		It("rejects a negative cleanup", func() {
			neg := -1
			ms := &config.MissionScratchpad{Cleanup: &neg}
			Expect(ms.Validate()).To(MatchError(ContainSubstring("cleanup must be >= 0")))
		})

		It("rejects a `path` attribute", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  scratchpad { path = "./x" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("memory + scratchpad on the same mission", func() {
		It("allows both", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory     { description = "long-term" }
  scratchpad { description = "per-run" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Memory).NotTo(BeNil())
			Expect(cfg.Missions[0].Scratchpad).NotTo(BeNil())
		})
	})

	Describe("deprecated DSL surfaces", func() {
		It("rejects the old `folder { ... }` block with a pointer at the new syntax", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  folder { path = "./x" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("`folder { ... }` block is no longer supported"))
		})

		It("rejects the old `run_folder { ... }` block", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  run_folder { base = "./x" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("`run_folder { ... }` block is no longer supported"))
		})

		It("rejects the old `folders = ...` attribute", func() {
			hcl := fullBaseHCL() + `
memory "ref" {}
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  folders   = [memories.ref]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("`folders` attribute is no longer supported"))
		})
	})
})
