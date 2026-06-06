package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Block name validation", func() {
	loadInline := func(hcl string) error {
		dir, _ := writeFixture("blocknames.hcl", hcl)
		_, err := config.LoadAndValidate(dir)
		return err
	}

	Context("invalid block labels", func() {
		It("rejects an uppercase letter in a model name", func() {
			hcl := minimalVarsHCL() + `
model "Anthropic" {
  provider = "anthropic"
  api_key  = vars.test_api_key
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("model name \"Anthropic\" is invalid"))
		})

		It("rejects a hyphen in an agent name", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "test-agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  tools       = [builtins.http.get]
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agent name \"test-agent\" is invalid"))
		})

		It("rejects a leading digit in a variable name", func() {
			hcl := `
variable "1key" {
  default = "x"
}
storage {
  backend = "sqlite"
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("variable name \"1key\" is invalid"))
		})

		It("rejects an invalid task name and points at the mission", func() {
			hcl := fullBaseHCL() + `
mission "good_mission" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  task "Bad Task" {
    objective = "Do something"
  }
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mission 'good_mission'"))
			Expect(err.Error()).To(ContainSubstring("task name \"Bad Task\" is invalid"))
		})

		It("rejects an invalid mission name", func() {
			hcl := fullBaseHCL() + `
mission "Bad-Mission" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  task "step" {
    objective = "Do something"
  }
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mission name \"Bad-Mission\" is invalid"))
		})

		It("rejects an invalid dataset name and points at the mission", func() {
			hcl := fullBaseHCL() + `
mission "good_mission" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  dataset "Bad Set" {
    items = [{ name = "one" }]
  }

  task "step" {
    objective = "Do something"
  }
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mission 'good_mission'"))
			Expect(err.Error()).To(ContainSubstring("dataset name \"Bad Set\" is invalid"))
		})

		It("rejects an invalid mission-scoped agent name", func() {
			hcl := fullBaseHCL() + `
mission "good_mission" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }

  agent "Bad-Local" {
    model       = models.anthropic.claude_sonnet_4
    personality = "Helpful"
    tools       = [builtins.http.get]
  }

  agents = [agents.test_agent]

  task "step" {
    objective = "Do something"
  }
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mission 'good_mission'"))
			Expect(err.Error()).To(ContainSubstring("agent name \"Bad-Local\" is invalid"))
		})

		It("rejects an invalid memory name", func() {
			hcl := minimalVarsHCL() + `
memory "Bad-Memory" {
  description = "notes"
}
`
			err := loadInline(hcl)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("memory name \"Bad-Memory\" is invalid"))
		})
	})

	Context("valid block labels", func() {
		It("accepts lowercase, digits, and underscores", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "agent_2" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  tools       = [builtins.http.get]
}

mission "pipeline_v2" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.agent_2]

  task "step_1" {
    objective = "First step"
  }
}
`
			err := loadInline(hcl)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
