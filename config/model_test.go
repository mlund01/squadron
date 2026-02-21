package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Model", func() {

	Describe("parsing", func() {
		It("parses a model with valid provider and models", func() {
			hcl := minimalVarsHCL() + `
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4", "claude_opus_4"]
  api_key        = vars.test_api_key
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Models).To(HaveLen(1))
			Expect(cfg.Models[0].Name).To(Equal("anthropic"))
			Expect(cfg.Models[0].Provider).To(Equal(config.ProviderAnthropic))
			Expect(cfg.Models[0].AllowedModels).To(ConsistOf("claude_sonnet_4", "claude_opus_4"))
			Expect(cfg.Models[0].APIKey).To(Equal("test-key-123"))
		})

		It("parses models for all three providers", func() {
			hcl := `
variable "key" { default = "k" }
model "openai" {
  provider       = "openai"
  allowed_models = ["gpt_4o"]
  api_key        = vars.key
}
model "gemini" {
  provider       = "gemini"
  allowed_models = ["gemini_2_0_flash"]
  api_key        = vars.key
}
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.key
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Models).To(HaveLen(3))
		})

		It("resolves api_key from variable reference", func() {
			hcl := `
variable "mykey" { default = "resolved-key" }
model "test" {
  provider       = "openai"
  allowed_models = ["gpt_4o"]
  api_key        = vars.mykey
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Models[0].APIKey).To(Equal("resolved-key"))
		})
	})

	Describe("Validate", func() {
		It("rejects unsupported provider", func() {
			hcl := minimalVarsHCL() + `
model "bad" {
  provider       = "llama"
  allowed_models = ["llama_7b"]
  api_key        = vars.test_api_key
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Unsupported provider"))
		})

		It("rejects unsupported model key for a valid provider", func() {
			hcl := minimalVarsHCL() + `
model "openai" {
  provider       = "openai"
  allowed_models = ["gpt_4o", "nonexistent_model"]
  api_key        = vars.test_api_key
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Unsupported model"))
			Expect(err.Error()).To(ContainSubstring("nonexistent_model"))
		})

		It("accepts all supported openai model keys", func() {
			m := config.Model{
				Name:          "openai",
				Provider:      config.ProviderOpenAI,
				AllowedModels: []string{"gpt_4o", "gpt_4o_mini", "gpt_4_turbo", "o1", "o1_mini", "o3_mini"},
				APIKey:        "k",
			}
			Expect(m.Validate()).To(Succeed())
		})

		It("accepts all supported gemini model keys", func() {
			m := config.Model{
				Name:          "gemini",
				Provider:      config.ProviderGemini,
				AllowedModels: []string{"gemini_2_0_flash", "gemini_1_5_pro", "gemini_1_5_flash", "gemini_2_0_flash_exp"},
				APIKey:        "k",
			}
			Expect(m.Validate()).To(Succeed())
		})

		It("accepts all supported anthropic model keys", func() {
			m := config.Model{
				Name:          "anthropic",
				Provider:      config.ProviderAnthropic,
				AllowedModels: []string{"claude_sonnet_4", "claude_opus_4", "claude_3_5_haiku", "claude_3_5_sonnet"},
				APIKey:        "k",
			}
			Expect(m.Validate()).To(Succeed())
		})
	})
})
