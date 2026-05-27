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
			Expect(cfg.Memories[0].Description).To(Equal("Research docs"))
			Expect(cfg.Missions[0].Memories).To(ConsistOf("research"))
		})

		It("requires a description", func() {
			hcl := fullBaseHCL() + `
memory "research" {}
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

		It("rejects the reserved name 'memory'", func() {
			m := config.Memory{Name: "memory", Description: "x"}
			err := m.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects the reserved name 'scratchpad'", func() {
			m := config.Memory{Name: "scratchpad", Description: "x"}
			err := m.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects an `editable` attribute (all memories are editable now)", func() {
			hcl := fullBaseHCL() + `
memory "research" {
  description = "x"
  editable    = true
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
  description = "x"
  path        = "./data"
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
		It("parses a memory block with a description", func() {
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
			Expect(cfg.Missions[0].Scratchpad).To(BeFalse())
		})

		It("requires a description", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory {}
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
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

		It("rejects a `path` attribute", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { description = "x"; path = "./x" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("mission scratchpad attribute", func() {
		It("defaults to false when not set", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Scratchpad).To(BeFalse())
		})

		It("accepts scratchpad = true", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  scratchpad = true
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Scratchpad).To(BeTrue())
		})

	})

	Describe("memory + scratchpad on the same mission", func() {
		It("allows both", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory     { description = "long-term" }
  scratchpad = true
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Memory).NotTo(BeNil())
			Expect(cfg.Missions[0].Scratchpad).To(BeTrue())
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
memory "ref" { description = "x" }
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
