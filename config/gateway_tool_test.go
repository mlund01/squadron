package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("builtins.gateway.post tool", func() {
	// An agent that uses the gateway post tool.
	base := func(extra string) string {
		return minimalVarsHCL() + minimalModelHCL() + extra + `
agent "poster" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  tools       = [builtins.gateway.post]
}
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.poster]
  task "run" { objective = "post a message" }
}
`
	}

	It("is rejected when no gateway is configured", func() {
		_, f := writeFixture("config.hcl", base(""))
		_, err := config.LoadAndValidate(f)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("builtins.gateway.post"))
	})

	It("is accepted when a gateway is configured", func() {
		dir := writeFixtures(map[string]string{
			"config.hcl":  base(""),
			"gateway.hcl": gatewayBlockHCL(),
		})
		_, err := config.LoadAndValidate(dir)
		Expect(err).NotTo(HaveOccurred())
	})
})
