package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LoadAndValidate (end-to-end)", func() {

	Context("single-file config", func() {
		It("succeeds with a complete valid config", func() {
			hcl := fullBaseHCL() + `
mission "test_mission" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.test_agent]

  task "first" {
    objective = "Do something"
  }
}
`
			dir, _ := writeFixture("all.hcl", hcl)
			cfg, err := config.LoadAndValidate(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Models).To(HaveLen(1))
			Expect(cfg.Agents).To(HaveLen(1))
			Expect(cfg.Missions).To(HaveLen(1))
		})
	})

	Context("multi-file directory", func() {
		It("succeeds loading separate files", func() {
			dir := writeFixtures(map[string]string{
				"variables.hcl": minimalVarsHCL(),
				"models.hcl":    minimalModelHCL(),
				"agents.hcl":    minimalAgentHCL(),
				"missions.hcl": `
mission "pipeline" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.test_agent]

  task "step_one" {
    objective = "First step"
  }

  task "step_two" {
    objective  = "Second step"
    depends_on = [tasks.step_one]
  }
}
`,
			})

			cfg, err := config.LoadAndValidate(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Models).To(HaveLen(1))
			Expect(cfg.Agents).To(HaveLen(1))
			Expect(cfg.Missions).To(HaveLen(1))
			Expect(cfg.Missions[0].Tasks).To(HaveLen(2))
		})
	})

	Context("variable validation errors", func() {
		It("rejects a secret variable with a default", func() {
			hcl := minimalVarsHCL() + `
variable "bad_secret" {
  secret  = true
  default = "oops"
}
` + minimalModelHCL() + minimalAgentHCL()

			dir, _ := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(dir)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secret"))
			Expect(err.Error()).To(ContainSubstring("bad_secret"))
		})
	})

	Context("model validation errors", func() {
		It("rejects an unsupported provider", func() {
			hcl := minimalVarsHCL() + `
model "bad" {
  provider       = "llama"
  allowed_models = ["llama_3"]
  api_key        = vars.test_api_key
}

agent "test_agent" {
  model       = models.bad.llama_3
  personality = "Helpful"
  role        = "Test"
  tools       = [plugins.bash.bash]
}
`
			dir, _ := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(dir)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Unsupported provider"))
		})
	})

	Context("mission validation errors", func() {
		It("rejects a task dependency cycle", func() {
			hcl := fullBaseHCL() + `
mission "cycled" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.test_agent]

  task "a" {
    objective  = "A"
    depends_on = [tasks.b]
  }

  task "b" {
    objective  = "B"
    depends_on = [tasks.a]
  }
}
`
			dir, _ := writeFixture("config.hcl", hcl)
			_, err := config.LoadAndValidate(dir)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cycle"))
		})
	})

	Context("agent tool ref errors", func() {
		It("rejects an unknown tool reference in an agent", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "bad_agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  role        = "Test"
  tools       = [plugins.bash.bash]
}
`
			dir, _ := writeFixture("config.hcl", hcl)

			// Load succeeds but we'll manually add a bad tool ref and validate
			cfg, err := config.Load(dir)
			Expect(err).NotTo(HaveOccurred())
			// Inject a bad tool ref
			cfg.Agents[0].Tools = append(cfg.Agents[0].Tools, "plugins.nonexistent.tool")
			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown tool"))
			Expect(err.Error()).To(ContainSubstring("plugins.nonexistent.tool"))
		})
	})

	Context("plugin warnings with valid config", func() {
		It("succeeds but populates PluginWarnings", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + minimalAgentHCL() + `
plugin "missing_plugin" {
  source  = "github.com/fake/plugin"
  version = "v1.0.0"
}
`
			dir, _ := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.PluginWarnings).NotTo(BeEmpty())
			Expect(cfg.PluginWarnings[0]).To(ContainSubstring("missing_plugin"))
		})
	})

	Context("custom tool internal name conflict", func() {
		It("rejects a custom tool named after an internal tool", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements  = "plugins.http.get"
  description = "Get weather"
  url         = "https://api.weather.com"
}

agent "test_agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  role        = "Test"
  tools       = [plugins.bash.bash, tools.weather]
}
`
			dir, _ := writeFixture("config.hcl", hcl)
			cfg, err := config.Load(dir)
			Expect(err).NotTo(HaveOccurred())

			// Rename the custom tool to conflict with an internal tool (bash, http_get, etc.)
			// Also update the agent's tool ref so it doesn't fail on "unknown tool" first
			cfg.CustomTools[0].Name = "bash"
			cfg.Agents[0].Tools = []string{"plugins.bash.bash", "tools.bash"}
			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("conflicts with internal tool"))
		})
	})

	Context("complete config with all block types", func() {
		It("handles vars, models, custom tools, agents, and missions together", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements  = "plugins.http.get"
  description = "Get weather data"
  url         = "https://api.weather.com/forecast"
}

agent "researcher" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Research focused"
  role        = "Researcher"
  tools       = [plugins.bash.bash, tools.weather]
}

mission "research_pipeline" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.researcher]

  input "city" {
    type        = "string"
    description = "City to research"
  }

  dataset "cities" {
    description = "Cities to process"

    schema {
      field "name" {
        type     = "string"
        required = true
      }
    }
  }

  task "gather" {
    objective = "Gather data for ${inputs.city}"
  }

  task "analyze" {
    objective  = "Analyze gathered data"
    depends_on = [tasks.gather]

    output {
      field "summary" {
        type     = "string"
        required = true
      }
    }
  }
}
`
			dir, _ := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Models).To(HaveLen(1))
			Expect(cfg.CustomTools).To(HaveLen(1))
			Expect(cfg.Agents).To(HaveLen(1))
			Expect(cfg.Missions).To(HaveLen(1))
			Expect(cfg.Missions[0].Inputs).To(HaveLen(1))
			Expect(cfg.Missions[0].Datasets).To(HaveLen(1))
			Expect(cfg.Missions[0].Tasks).To(HaveLen(2))
			Expect(cfg.Missions[0].Tasks[1].Output).NotTo(BeNil())
			Expect(cfg.Missions[0].Tasks[1].Output.Fields).To(HaveLen(1))
		})
	})
})
