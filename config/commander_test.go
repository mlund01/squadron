package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Commander Config", func() {

	Describe("Parsing", func() {
		It("parses a commander block with all fields", func() {
			hcl := `
commander {
  url                = "ws://localhost:8080/ws"
  instance_name      = "production-scraper"
  auto_reconnect     = true
  reconnect_interval = 10
}
`
			_, f := writeFixture("commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Commander).NotTo(BeNil())
			Expect(cfg.Commander.URL).To(Equal("ws://localhost:8080/ws"))
			Expect(cfg.Commander.InstanceName).To(Equal("production-scraper"))
			Expect(cfg.Commander.AutoReconnect).To(BeTrue())
			Expect(cfg.Commander.ReconnectInterval).To(Equal(10))
		})

		It("applies defaults for optional fields", func() {
			hcl := `
commander {
  url           = "ws://localhost:8080/ws"
  instance_name = "my-instance"
}
`
			_, f := writeFixture("commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Commander).NotTo(BeNil())
			Expect(cfg.Commander.AutoReconnect).To(BeFalse())
			Expect(cfg.Commander.ReconnectInterval).To(Equal(5))
		})

		It("leaves Commander nil when no block is present", func() {
			hcl := minimalVarsHCL()
			_, f := writeFixture("no-commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Commander).To(BeNil())
		})

		It("supports variable interpolation in commander fields", func() {
			hcl := `
variable "commander_url" {
  default = "ws://staging:9090/ws"
}

commander {
  url           = vars.commander_url
  instance_name = "staging-worker"
}
`
			_, f := writeFixture("commander-vars.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Commander).NotTo(BeNil())
			Expect(cfg.Commander.URL).To(Equal("ws://staging:9090/ws"))
		})
	})

	Describe("Validation", func() {
		It("fails when url is missing", func() {
			hcl := `
commander {
  instance_name = "test"
}
`
			_, f := writeFixture("bad-commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("url is required"))
		})

		It("fails when instance_name is missing", func() {
			hcl := `
commander {
  url = "ws://localhost:8080/ws"
}
`
			_, f := writeFixture("bad-commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("instance_name is required"))
		})

		It("passes validation with all required fields", func() {
			hcl := `
commander {
  url           = "ws://localhost:8080/ws"
  instance_name = "valid-instance"
}
`
			_, f := writeFixture("good-commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).NotTo(HaveOccurred())
		})

		It("passes validation when commander block is absent", func() {
			hcl := minimalVarsHCL()
			_, f := writeFixture("no-commander.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
