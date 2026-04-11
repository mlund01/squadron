package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Low-level behavior of the load() function (path resolution, file-type
// allowlist, error messages) is unit-tested in config/functions/load_test.go.
// These tests only verify that load() is wired into the HCL eval contexts
// where it's supposed to be exposed — each It covers one such context.
//
// Task objectives and other deferred expressions preserve the raw HCL source
// and are re-evaluated at runtime without load() in scope, so those paths are
// deliberately not covered.
var _ = Describe("load() eval context wiring", func() {

	It("is available in skill.instructions", func() {
		dir := writeFixtures(map[string]string{
			"skill.md": "skill body",
			"config.hcl": fullBaseHCL() + `
skill "s" {
  description  = "desc"
  instructions = load("./skill.md")
}
`,
		})
		cfg, err := config.LoadFile(dir + "/config.hcl")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Skills).To(HaveLen(1))
		Expect(cfg.Skills[0].Instructions).To(Equal("skill body"))
	})

	It("is available in agent.personality", func() {
		dir := writeFixtures(map[string]string{
			"persona.md": "stoic",
			"config.hcl": minimalVarsHCL() + minimalModelHCL() + `
agent "custom" {
  model       = models.anthropic.claude_sonnet_4
  personality = load("./persona.md")
  role        = "worker"
  tools       = [builtins.http.get]
}
`,
		})
		cfg, err := config.LoadFile(dir + "/config.hcl")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Agents[0].Personality).To(Equal("stoic"))
	})

	It("is available in agent-scoped skill.instructions", func() {
		dir := writeFixtures(map[string]string{
			"helper.md": "helper body",
			"config.hcl": minimalVarsHCL() + minimalModelHCL() + `
agent "custom" {
  model       = models.anthropic.claude_sonnet_4
  personality = "helpful"
  role        = "worker"

  skill "helper" {
    description  = "helper"
    instructions = load("./helper.md")
  }
}
`,
		})
		cfg, err := config.LoadFile(dir + "/config.hcl")
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Agents[0].LocalSkills).To(HaveLen(1))
		Expect(cfg.Agents[0].LocalSkills[0].Instructions).To(Equal("helper body"))
	})
})
