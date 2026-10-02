package config_test

import (
	"fmt"
	"strings"

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

		It("rejects names with path separators or '..' (path-traversal guard)", func() {
			for _, bad := range []string{"../escape", "foo/bar", "foo\\bar", "..", ".", ".hidden", "a..b"} {
				m := config.Memory{Name: bad, Description: "x"}
				err := m.Validate()
				Expect(err).To(HaveOccurred(), "expected %q to be rejected", bad)
			}
		})

		It("rejects a shared memory whose label collides with a mission name", func() {
			hcl := fullBaseHCL() + `
memory "analyze" {
  description = "shared"
}
mission "analyze" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			// Cross-name collision is caught by Config.Validate (the wsbridge
			// browser keys both under the same string).
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("conflicts with mission"))
		})

		It("gives a clear error when a mission references an unknown shared memory", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  memories  = [memories.does_not_exist]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			// Must reference the missing name — not be a generic 'unknown
			// variable memories' (which is what happens without the always-
			// register-empty-namespace fix).
			Expect(err.Error()).To(ContainSubstring("does_not_exist"))
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
		It("parses schema and retention with access declared on tasks", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  models = [models.anthropic.claude_sonnet_4]
  memory {
    description = "Findings"
    retention_days = 30
    field "summary" { type = "string" }
    field "score" { type = "number" }
  }
  task "research" {
    objective = "go"
    memory_access = "read_write"
  }
  task "report" {
    objective = "summarize"
    memory_access = "read"
  }
  task "audit" { objective = "Check formatting" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			memory := cfg.Missions[0].Memory
			Expect(memory.HasPolicy()).To(BeTrue())
			Expect(memory.RetentionDays).To(Equal(30))
			Expect(memory.Fields).To(HaveLen(2))
			Expect(cfg.Missions[0].Tasks[0].MemoryAccess).To(Equal("read_write"))
			Expect(cfg.Missions[0].Tasks[1].MemoryAccess).To(Equal("read"))
			Expect(cfg.Missions[0].Tasks[2].MemoryAccess).To(BeEmpty())
		})

		It("rejects incomplete schema and duplicate fields", func() {
			memory := &config.MissionMemory{
				Description: "Findings", RetentionDays: 30,
			}
			Expect(memory.Validate()).To(MatchError(ContainSubstring("field")))
			memory.Fields = []config.MissionMemoryField{{Name: "summary", Type: "string"}}
			Expect(memory.Validate()).To(Succeed())
			memory.Fields = append(memory.Fields, memory.Fields[0])
			Expect(memory.Validate()).To(MatchError(ContainSubstring("unique")))
		})

		It("rejects memory schemas beyond runtime key and field limits", func() {
			memory := &config.MissionMemory{Description: "Findings", RetentionDays: 30,
				Fields: []config.MissionMemoryField{{Name: strings.Repeat("x", config.MaxMissionMemoryKeyBytes+1), Type: "string"}}}
			Expect(memory.Validate()).To(HaveOccurred())
			memory.Fields[0].Name = "summary"
			for len(memory.Fields) <= config.MaxMissionMemoryFields {
				memory.Fields = append(memory.Fields, config.MissionMemoryField{Name: fmt.Sprintf("key_%d", len(memory.Fields)), Type: "string"})
			}
			Expect(memory.Validate()).To(MatchError(ContainSubstring("more than")))
		})

		It("rejects task grants without a mission memory policy", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  models = [models.anthropic.claude_sonnet_4]
  task "research" {
    objective = "go"
    memory_access = "read_write"
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(MatchError(ContainSubstring("requires a mission memory policy")))
		})

		It("rejects unsupported task memory access", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  models = [models.anthropic.claude_sonnet_4]
  memory {
    description = "Findings"
    retention_days = 30
    field "summary" { type = "string" }
  }
  task "research" {
    objective = "go"
    memory_access = "write"
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(MatchError(ContainSubstring("must be read or read_write")))
		})

		It("rejects obsolete memory-level task grants", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  models = [models.anthropic.claude_sonnet_4]
  memory {
    description = "Findings"
    read_tasks = ["research"]
  }
  task "research" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})

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
			// Remediation must point at the actual new DSL, not a defunct
			// intermediate from earlier in this PR.
			Expect(err.Error()).To(ContainSubstring("scratchpad = true"))
		})

		It("points at the new memory DSL when rejecting the old `folder { ... }` block", func() {
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
			Expect(err.Error()).To(ContainSubstring(`memory { description`))
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
