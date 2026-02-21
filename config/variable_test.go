package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Variable", func() {

	Describe("parsing", func() {
		It("parses a variable with a default value", func() {
			_, f := writeFixture("vars.hcl", `variable "app_name" { default = "squadron" }`)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Variables[0].Name).To(Equal("app_name"))
			Expect(cfg.Variables[0].Default).To(Equal("squadron"))
			Expect(cfg.Variables[0].Secret).To(BeFalse())
		})

		It("parses a secret variable without a default", func() {
			_, f := writeFixture("vars.hcl", `variable "api_key" { secret = true }`)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(1))
			Expect(cfg.Variables[0].Secret).To(BeTrue())
			Expect(cfg.Variables[0].Default).To(BeEmpty())
		})

		It("parses a variable with no attributes", func() {
			_, f := writeFixture("vars.hcl", `variable "bare" {}`)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables[0].Name).To(Equal("bare"))
			Expect(cfg.Variables[0].Default).To(BeEmpty())
			Expect(cfg.Variables[0].Secret).To(BeFalse())
		})

		It("parses multiple variables", func() {
			hcl := `
variable "a" { default = "alpha" }
variable "b" { default = "beta" }
variable "c" { secret = true }
`
			_, f := writeFixture("vars.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Variables).To(HaveLen(3))
		})
	})

	Describe("Validate", func() {
		It("rejects secret variable with a default value", func() {
			hcl := `
variable "bad_secret" {
  secret  = true
  default = "oops"
}
`
			_, f := writeFixture("vars.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("bad_secret"))
			Expect(err.Error()).To(ContainSubstring("secret"))
		})

		It("accepts non-secret variable with a default", func() {
			hcl := `variable "ok_var" { default = "hello" }`
			_, f := writeFixture("vars.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Validate()).To(Succeed())
		})

		It("accepts secret variable without a default", func() {
			hcl := `variable "good_secret" { secret = true }`
			_, f := writeFixture("vars.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Validate()).To(Succeed())
		})
	})
})
