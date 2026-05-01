package config_test

import (
	"strings"

	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Reasoning HCL parsing", func() {
	Describe("agent reasoning attribute", func() {
		It("parses a valid level", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "test_agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  role        = "Test agent"
  reasoning   = "high"
  tools       = [builtins.http.get]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents).To(HaveLen(1))
			Expect(cfg.Agents[0].Reasoning).To(Equal("high"))
		})

		It("normalizes case during validation", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "test_agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  role        = "Test agent"
  reasoning   = "MEDIUM"
  tools       = [builtins.http.get]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Reasoning).To(Equal("medium"))
		})

		It("rejects an invalid value", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "test_agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  role        = "Test agent"
  reasoning   = "extreme"
  tools       = [builtins.http.get]
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid reasoning"))
		})

		It("defaults to empty when omitted", func() {
			_, f := writeFixture("config.hcl", fullBaseHCL())
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Reasoning).To(BeEmpty())
		})
	})

	Describe("commander reasoning attribute", func() {
		It("parses on the commander block", func() {
			hcl := fullBaseHCL() + `
mission "test_mission" {
  commander {
    model     = models.anthropic.claude_sonnet_4
    reasoning = "high"
  }
  agents = [agents.test_agent]
  task "only_task" {
    objective = "Do something"
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions).To(HaveLen(1))
			Expect(cfg.Missions[0].Commander.Reasoning).To(Equal("high"))
		})

		It("rejects an invalid value on the commander", func() {
			hcl := fullBaseHCL() + `
mission "test_mission" {
  commander {
    model     = models.anthropic.claude_sonnet_4
    reasoning = "extreme"
  }
  agents = [agents.test_agent]
  task "only_task" {
    objective = "Do something"
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid reasoning"))
		})
	})

	Describe("model reasoning_models attribute", func() {
		It("parses a list of alias keys", func() {
			hcl := minimalVarsHCL() + `
model "ollama" {
  provider = "ollama"
  base_url = "http://localhost:11434/v1"
  aliases = {
    deepseek_r1 = "deepseek-r1:7b"
    llama3      = "llama3:8b"
  }
  reasoning_models = ["deepseek_r1"]
}

agent "ollama_agent" {
  model       = models.ollama.deepseek_r1
  personality = "Local"
  role        = "Local model agent"
  tools       = [builtins.http.get]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			ollama := findModel(cfg, "ollama")
			Expect(ollama.ReasoningModels).To(Equal([]string{"deepseek_r1"}))
		})

		It("errors when entry references unknown alias", func() {
			hcl := minimalVarsHCL() + `
model "ollama" {
  provider = "ollama"
  base_url = "http://localhost:11434/v1"
  aliases = {
    llama3 = "llama3:8b"
  }
  reasoning_models = ["nonexistent"]
}

agent "ollama_agent" {
  model       = models.ollama.llama3
  personality = "Local"
  role        = "Local model agent"
  tools       = [builtins.http.get]
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("nonexistent"))
		})
	})
})

func findModel(cfg *config.Config, name string) *config.Model {
	for i := range cfg.Models {
		if cfg.Models[i].Name == name {
			return &cfg.Models[i]
		}
	}
	return nil
}
