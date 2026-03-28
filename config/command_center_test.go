package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Command Center Config", func() {

	Describe("Parsing", func() {
		It("parses a command_center block with all fields", func() {
			hcl := `
command_center {
  url                = "ws://localhost:8080/ws"
  instance_name      = "production-scraper"
  auto_reconnect     = true
  reconnect_interval = 10
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("command_center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CommandCenter).NotTo(BeNil())
			Expect(cfg.CommandCenter.URL).To(Equal("ws://localhost:8080/ws"))
			Expect(cfg.CommandCenter.InstanceName).To(Equal("production-scraper"))
			Expect(cfg.CommandCenter.AutoReconnect).To(BeTrue())
			Expect(cfg.CommandCenter.ReconnectInterval).To(Equal(10))
		})

		It("applies defaults for optional fields", func() {
			hcl := `
command_center {
  url           = "ws://localhost:8080/ws"
  instance_name = "my-instance"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("command_center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CommandCenter).NotTo(BeNil())
			Expect(cfg.CommandCenter.AutoReconnect).To(BeFalse())
			Expect(cfg.CommandCenter.ReconnectInterval).To(Equal(5))
		})

		It("leaves CommandCenter nil when no block is present", func() {
			hcl := minimalVarsHCL()
			_, f := writeFixture("no-command-center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CommandCenter).To(BeNil())
		})

		It("supports variable interpolation in command_center fields", func() {
			hcl := `
variable "cc_url" {
  default = "ws://staging:9090/ws"
}

command_center {
  url           = vars.cc_url
  instance_name = "staging-worker"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("command-center-vars.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CommandCenter).NotTo(BeNil())
			Expect(cfg.CommandCenter.URL).To(Equal("ws://staging:9090/ws"))
		})
	})

	Describe("Validation", func() {
		It("fails when url is missing", func() {
			hcl := `
command_center {
  instance_name = "test"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("bad-command-center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("url is required"))
		})

		It("fails when instance_name is missing", func() {
			hcl := `
command_center {
  url = "ws://localhost:8080/ws"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("bad-command-center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("instance_name is required"))
		})

		It("passes validation with all required fields", func() {
			hcl := `
command_center {
  url           = "ws://localhost:8080/ws"
  instance_name = "valid-instance"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("good-command-center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).NotTo(HaveOccurred())
		})

		It("passes validation when command_center block is absent", func() {
			hcl := minimalVarsHCL()
			_, f := writeFixture("no-command-center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
