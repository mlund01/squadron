package config_test

import (
	"os"
	"path/filepath"
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Config Loading", func() {

	Describe("Load", func() {
		It("routes to LoadFile for a file path", func() {
			_, f := writeFixture("vars.hcl", `variable "x" { default = "val" }`)
			cfg, err := config.Load(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Variables[0].Name).To(Equal("x"))
		})

		It("routes to LoadDir for a directory path", func() {
			dir := writeFixtures(map[string]string{
				"variables.hcl": `variable "a" { default = "1" }`,
				"models.hcl": minimalVarsHCL() + `
model "test" {
  provider       = "openai"
  allowed_models = ["gpt_4o"]
  api_key        = vars.test_api_key
}
`,
			})
			cfg, err := config.Load(dir)
			Expect(err).NotTo(HaveOccurred())
			// Variables from both files (test_api_key from minimalVarsHCL + "a")
			Expect(len(cfg.Variables)).To(BeNumerically(">=", 1))
			Expect(cfg.Models).To(HaveLen(1))
		})

		It("returns error for nonexistent path", func() {
			_, err := config.Load("/nonexistent/path/config.hcl")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("LoadFile", func() {
		It("parses a single HCL file with multiple block types", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + minimalAgentHCL()
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Models).To(HaveLen(1))
			Expect(cfg.Agents).To(HaveLen(1))
		})

		It("returns parse error for invalid HCL syntax", func() {
			_, f := writeFixture("bad.hcl", `model { missing label and brace`)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("LoadDir", func() {
		It("loads all .hcl files from the directory", func() {
			dir := writeFixtures(map[string]string{
				"variables.hcl": `variable "v1" { default = "a" }`,
				"models.hcl": `
variable "k" { default = "key" }
model "m1" {
  provider       = "openai"
  allowed_models = ["gpt_4o"]
  api_key        = vars.k
}
`,
			})
			cfg, err := config.LoadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Models).To(HaveLen(1))
		})

		It("ignores non-.hcl files", func() {
			dir := writeFixtures(map[string]string{
				"config.hcl":    `variable "x" { default = "y" }`,
				"readme.txt":    `This is not HCL`,
				"data.json":     `{"key": "value"}`,
			})
			cfg, err := config.LoadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
		})

		It("returns empty config for directory with no .hcl files", func() {
			dir := GinkgoT().TempDir()
			// Write a non-HCL file so the dir isn't completely empty
			err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644)
			Expect(err).NotTo(HaveOccurred())
			cfg, err := config.LoadDir(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(BeEmpty())
			Expect(cfg.Models).To(BeEmpty())
			Expect(cfg.Agents).To(BeEmpty())
		})
	})

	Describe("Staged evaluation order", func() {
		It("resolves variable references in model blocks", func() {
			hcl := `
variable "my_key" { default = "resolved-api-key" }
model "test" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.my_key
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Models[0].APIKey).To(Equal("resolved-api-key"))
		})

		It("resolves model references in agent blocks", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "a" {
  model       = models.anthropic.claude_sonnet_4
  personality = "x"
  role        = "y"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Model).To(Equal("claude_sonnet_4"))
		})

		It("resolves agent references in mission blocks", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.test_agent]
  task "t" { objective = "Do work" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Agents).To(ConsistOf("test_agent"))
		})

		It("resolves task dependency references", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.test_agent]
  task "first" { objective = "Step 1" }
  task "second" {
    objective  = "Step 2"
    depends_on = [tasks.first]
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Tasks[1].DependsOn).To(ConsistOf("first"))
		})
	})

	Describe("ResolvedVars", func() {
		It("populates ResolvedVars map from variable defaults", func() {
			hcl := `variable "app_name" { default = "myapp" }`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ResolvedVars).To(HaveKey("app_name"))
			Expect(cfg.ResolvedVars["app_name"].AsString()).To(Equal("myapp"))
		})
	})

	Describe("Plugin loading", func() {
		It("populates PluginWarnings when plugin binary is not found", func() {
			hcl := minimalVarsHCL() + `
plugin "nonexistent" {
  source  = "github.com/example/nonexistent"
  version = "v1.0.0"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.PluginWarnings).NotTo(BeEmpty())
			Expect(cfg.LoadedPlugins).To(BeEmpty())
		})

		It("still loads other config even when plugin fails", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
plugin "ghost" {
  source  = "github.com/example/ghost"
  version = "v2.0.0"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Models).To(HaveLen(1))
			Expect(cfg.PluginWarnings).NotTo(BeEmpty())
		})
	})
})
