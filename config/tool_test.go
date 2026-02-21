package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CustomTool", func() {

	Describe("parsing", func() {
		It("parses a tool implementing plugins.http.get with inputs and dynamic fields", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements  = plugins.http.get
  description = "Get weather for a city"
  inputs {
    field "city" {
      type        = "string"
      description = "City name"
      required    = true
    }
  }
  url = "https://wttr.in/${inputs.city}?format=3"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CustomTools).To(HaveLen(1))
			Expect(cfg.CustomTools[0].Name).To(Equal("weather"))
			Expect(cfg.CustomTools[0].Implements).To(Equal("plugins.http.get"))
			Expect(cfg.CustomTools[0].Description).To(Equal("Get weather for a city"))
			Expect(cfg.CustomTools[0].Inputs).NotTo(BeNil())
			Expect(cfg.CustomTools[0].Inputs.Fields).To(HaveLen(1))
			Expect(cfg.CustomTools[0].Inputs.Fields[0].Name).To(Equal("city"))
			Expect(cfg.CustomTools[0].Inputs.Fields[0].Type).To(Equal("string"))
			Expect(cfg.CustomTools[0].Inputs.Fields[0].Required).To(BeTrue())
			Expect(cfg.CustomTools[0].FieldExprs).To(HaveKey("url"))
		})

		It("parses a tool with http.post and body field", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "create_todo" {
  implements  = plugins.http.post
  description = "Create a todo"
  inputs {
    field "title" {
      type     = "string"
      required = true
    }
  }
  url  = "https://example.com/todos"
  body = {
    title     = inputs.title
    completed = false
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CustomTools[0].Implements).To(Equal("plugins.http.post"))
			Expect(cfg.CustomTools[0].FieldExprs).To(HaveKey("url"))
			Expect(cfg.CustomTools[0].FieldExprs).To(HaveKey("body"))
		})

		It("parses a tool with no inputs block", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "hello" {
  implements = plugins.bash.bash
  command    = "echo hello"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CustomTools[0].Inputs).To(BeNil())
			Expect(cfg.CustomTools[0].FieldExprs).To(HaveKey("command"))
		})

		It("parses multiple custom tools", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "tool_a" {
  implements = plugins.http.get
  url = "https://example.com/a"
}
tool "tool_b" {
  implements = plugins.http.get
  url = "https://example.com/b"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CustomTools).To(HaveLen(2))
		})
	})

	Describe("Validate", func() {
		It("accepts tool with plugins.* implements format", func() {
			t := config.CustomTool{Name: "mytool", Implements: "plugins.http.get"}
			Expect(t.Validate()).To(Succeed())
		})

		It("rejects tool without implements", func() {
			t := config.CustomTool{Name: "mytool", Implements: ""}
			err := t.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("implements is required"))
		})

		It("rejects tool with non-plugins.* implements format", func() {
			t := config.CustomTool{Name: "mytool", Implements: "bash"}
			err := t.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("plugins.{namespace}.{tool} format"))
		})

		It("rejects tool with legacy format implements", func() {
			t := config.CustomTool{Name: "mytool", Implements: "http_get"}
			err := t.Validate()
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("IsPluginTool / GetPluginToolRef", func() {
		It("returns true for plugins.* implements", func() {
			t := config.CustomTool{Implements: "plugins.bash.bash"}
			Expect(t.IsPluginTool()).To(BeTrue())
			pName, tName, ok := t.GetPluginToolRef()
			Expect(ok).To(BeTrue())
			Expect(pName).To(Equal("bash"))
			Expect(tName).To(Equal("bash"))
		})

		It("parses http plugin tool ref correctly", func() {
			t := config.CustomTool{Implements: "plugins.http.get"}
			pName, tName, ok := t.GetPluginToolRef()
			Expect(ok).To(BeTrue())
			Expect(pName).To(Equal("http"))
			Expect(tName).To(Equal("get"))
		})

		It("returns false for non-plugins implements", func() {
			t := config.CustomTool{Implements: "some_tool"}
			Expect(t.IsPluginTool()).To(BeFalse())
			_, _, ok := t.GetPluginToolRef()
			Expect(ok).To(BeFalse())
		})
	})

	Describe("Config.Validate rejects internal tool name conflict", func() {
		It("rejects a custom tool named 'bash'", func() {
			cfg := &config.Config{
				CustomTools: []config.CustomTool{
					{Name: "bash", Implements: "plugins.http.get"},
				},
			}
			err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("conflicts with internal tool"))
		})
	})

	Describe("agent references custom tools", func() {
		It("validates tools.* references to defined custom tools", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements  = plugins.http.get
  description = "Weather lookup"
  inputs {
    field "city" {
      type     = "string"
      required = true
    }
  }
  url = "https://wttr.in/${inputs.city}?format=3"
}
agent "tooluser" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Test"
  role        = "Tester"
  tools       = [plugins.bash.bash, tools.weather]
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Tools).To(ContainElement("tools.weather"))
		})
	})
})
