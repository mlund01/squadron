package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Agent", func() {

	Describe("parsing", func() {
		It("parses an agent with model reference and internal tools", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "helper" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Friendly and precise"
  role        = "General assistant"
  tools       = [plugins.bash.bash, plugins.http.get]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents).To(HaveLen(1))
			Expect(cfg.Agents[0].Name).To(Equal("helper"))
			Expect(cfg.Agents[0].Model).To(Equal("claude_sonnet_4"))
			Expect(cfg.Agents[0].Personality).To(Equal("Friendly and precise"))
			Expect(cfg.Agents[0].Role).To(Equal("General assistant"))
			Expect(cfg.Agents[0].Tools).To(ConsistOf("plugins.bash.bash", "plugins.http.get"))
		})

		It("parses an agent with pruning block", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "pruned" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Efficient"
  role        = "Pruning tester"
  tools       = [plugins.bash.bash]
  pruning {
    single_tool_limit = 2
    all_tool_limit    = 5
    turn_limit        = 10
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Pruning).NotTo(BeNil())
			Expect(cfg.Agents[0].GetSingleToolLimit()).To(Equal(2))
			Expect(cfg.Agents[0].GetAllToolLimit()).To(Equal(5))
			Expect(cfg.Agents[0].GetTurnLimit()).To(Equal(10))
		})

		It("parses an agent with compaction block", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "compacted" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Concise"
  role        = "Compaction tester"
  compaction {
    token_limit    = 5000
    turn_retention = 3
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Compaction).NotTo(BeNil())
			Expect(cfg.Agents[0].Compaction.TokenLimit).To(Equal(5000))
			Expect(cfg.Agents[0].Compaction.TurnRetention).To(Equal(3))
		})

		It("parses an agent with no tools", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "toolless" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Thoughtful"
  role        = "A chat-only agent"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Tools).To(BeEmpty())
		})

		It("defaults pruning accessors to 0 when no pruning block", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "no_pruning" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Simple"
  role        = "Basic agent"
  tools       = [plugins.bash.bash]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Pruning).To(BeNil())
			Expect(cfg.Agents[0].GetSingleToolLimit()).To(Equal(0))
			Expect(cfg.Agents[0].GetAllToolLimit()).To(Equal(0))
			Expect(cfg.Agents[0].GetTurnLimit()).To(Equal(0))
		})
	})

	Describe("Validate (tool references via Config.Validate)", func() {
		It("accepts agent with valid internal plugin tool references", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "valid_tools" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helper"
  role        = "Tool user"
  tools       = [plugins.bash.bash, plugins.http.get, plugins.http.post]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Tools).To(HaveLen(3))
		})

		It("accepts all HTTP plugin tools", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "http_all" {
  model       = models.anthropic.claude_sonnet_4
  personality = "API master"
  role        = "API caller"
  tools       = [plugins.http.get, plugins.http.post, plugins.http.put, plugins.http.patch, plugins.http.delete]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Tools).To(HaveLen(5))
		})

		It("accepts plugins.http.all reference", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "all_http" {
  model       = models.anthropic.claude_sonnet_4
  personality = "HTTP master"
  role        = "API caller"
  tools       = [plugins.http.all]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Tools).To(ContainElement("plugins.http.all"))
		})
	})

	Describe("ResolveModel", func() {
		It("resolves model key to the correct provider and model", func() {
			agent := config.Agent{Model: "claude_sonnet_4"}
			models := []config.Model{
				{
					Name:          "anthropic",
					Provider:      config.ProviderAnthropic,
					AllowedModels: []string{"claude_sonnet_4"},
					APIKey:        "k",
				},
			}
			m, actualModel, err := agent.ResolveModel(models)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Name).To(Equal("anthropic"))
			Expect(actualModel).To(Equal("claude-sonnet-4-20250514"))
		})

		It("returns error for unknown model key", func() {
			agent := config.Agent{Model: "nonexistent"}
			models := []config.Model{
				{
					Name:          "anthropic",
					Provider:      config.ProviderAnthropic,
					AllowedModels: []string{"claude_sonnet_4"},
					APIKey:        "k",
				},
			}
			_, _, err := agent.ResolveModel(models)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nonexistent"))
		})
	})
})
