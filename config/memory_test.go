package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Memory", func() {

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

		It("rejects the reserved name 'mission'", func() {
			m := config.Memory{Name: "mission"}
			err := m.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects the reserved name 'run'", func() {
			m := config.Memory{Name: "run"}
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

	Describe("mission memory block", func() {
		It("parses a persistent memory block (default type)", func() {
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
			Expect(cfg.Missions[0].PersistentMemory).NotTo(BeNil())
			Expect(cfg.Missions[0].PersistentMemory.Type).To(Equal(config.MemoryTypePersistent))
			Expect(cfg.Missions[0].PersistentMemory.Description).To(Equal("Long-term notes"))
			Expect(cfg.Missions[0].EphemeralMemory).To(BeNil())
		})

		It("parses an explicit persistent type", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory {
    type        = "persistent"
    description = "x"
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].PersistentMemory).NotTo(BeNil())
		})

		It("parses an ephemeral memory block with default cleanup", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory {
    type        = "ephemeral"
    description = "Scratch"
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].EphemeralMemory).NotTo(BeNil())
			Expect(cfg.Missions[0].EphemeralMemory.Type).To(Equal(config.MemoryTypeEphemeral))
			Expect(cfg.Missions[0].EphemeralMemory.Cleanup).NotTo(BeNil())
			Expect(*cfg.Missions[0].EphemeralMemory.Cleanup).To(Equal(config.DefaultEphemeralCleanupDays))
			Expect(cfg.Missions[0].PersistentMemory).To(BeNil())
		})

		It("preserves an explicit cleanup = 0 on ephemeral memory", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory {
    type    = "ephemeral"
    cleanup = 0
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].EphemeralMemory.Cleanup).NotTo(BeNil())
			Expect(*cfg.Missions[0].EphemeralMemory.Cleanup).To(Equal(0))
		})

		It("allows one persistent + one ephemeral on the same mission", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { type = "persistent" }
  memory { type = "ephemeral" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].PersistentMemory).NotTo(BeNil())
			Expect(cfg.Missions[0].EphemeralMemory).NotTo(BeNil())
		})

		It("rejects two persistent memory blocks", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { type = "persistent" }
  memory { type = "persistent" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one persistent memory"))
		})

		It("rejects two ephemeral memory blocks", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { type = "ephemeral" }
  memory { type = "ephemeral" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one ephemeral memory"))
		})

		It("rejects an unknown type", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memory { type = "weird" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`type must be "persistent" or "ephemeral"`))
		})

		It("rejects cleanup on persistent memory", func() {
			mm := &config.MissionMemory{Type: "persistent", Cleanup: ptrInt(7)}
			Expect(mm.Validate()).To(MatchError(ContainSubstring("cleanup is only valid on ephemeral memory")))
		})

		It("rejects negative cleanup", func() {
			mm := &config.MissionMemory{Type: "ephemeral", Cleanup: ptrInt(-1)}
			Expect(mm.Validate()).To(MatchError(ContainSubstring("cleanup must be >= 0")))
		})

		It("rejects the old `path` attribute on a mission memory block", func() {
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

func ptrInt(v int) *int { return &v }
