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
  host               = "https://commander.example.com"
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
			Expect(cfg.CommandCenter.Host).To(Equal("https://commander.example.com"))
			Expect(cfg.CommandCenter.InstanceName).To(Equal("production-scraper"))
			Expect(cfg.CommandCenter.AutoReconnect).To(BeTrue())
			Expect(cfg.CommandCenter.ReconnectInterval).To(Equal(10))
		})

		It("applies defaults for optional fields", func() {
			hcl := `
command_center {
  host          = "http://localhost:8080"
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
variable "cc_host" {
  default = "https://staging.example.com"
}

command_center {
  host          = vars.cc_host
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
			Expect(cfg.CommandCenter.Host).To(Equal("https://staging.example.com"))
		})
	})

	Describe("Validation", func() {
		It("fails when host is missing", func() {
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
			Expect(err.Error()).To(ContainSubstring("host is required"))
		})

		It("fails when instance_name is missing", func() {
			hcl := `
command_center {
  host = "https://localhost:8080"
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

		It("rejects the legacy url field with a migration hint", func() {
			hcl := `
command_center {
  url           = "ws://localhost:8080/ws"
  instance_name = "test"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("legacy-command-center.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("deprecated"))
			Expect(err.Error()).To(ContainSubstring("host"))
		})

		It("rejects a host without an http(s) scheme", func() {
			hcl := `
command_center {
  host          = "ws://localhost:8080"
  instance_name = "test"
}

storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("bad-scheme.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("http or https"))
		})

		It("passes validation with all required fields", func() {
			hcl := `
command_center {
  host          = "https://localhost:8080"
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

	Describe("Derived URLs", func() {
		It("builds the WebSocket URL from an https host", func() {
			cc := &config.CommandCenterConfig{Host: "https://foo.com"}
			Expect(cc.WebSocketURL()).To(Equal("wss://foo.com/ws"))
			Expect(cc.OAuthRedirectURI()).To(Equal("https://foo.com/oauth/callback"))
		})

		It("builds the WebSocket URL from an http host", func() {
			cc := &config.CommandCenterConfig{Host: "http://localhost:8080"}
			Expect(cc.WebSocketURL()).To(Equal("ws://localhost:8080/ws"))
			Expect(cc.OAuthRedirectURI()).To(Equal("http://localhost:8080/oauth/callback"))
		})

		It("preserves a path prefix on the host", func() {
			cc := &config.CommandCenterConfig{Host: "https://foo.com/commander"}
			Expect(cc.WebSocketURL()).To(Equal("wss://foo.com/commander/ws"))
			Expect(cc.OAuthRedirectURI()).To(Equal("https://foo.com/commander/oauth/callback"))
		})

		It("tolerates a trailing slash on the host", func() {
			cc := &config.CommandCenterConfig{Host: "https://foo.com/"}
			Expect(cc.WebSocketURL()).To(Equal("wss://foo.com/ws"))
			Expect(cc.OAuthRedirectURI()).To(Equal("https://foo.com/oauth/callback"))
		})
	})
})
