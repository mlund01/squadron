package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Notification Config", func() {
	missionWith := func(notificationBlock string, extra string) string {
		return fullBaseHCL() + extra + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
` + notificationBlock + `
  task "run" { objective = "do the thing" }
}
`
	}

	Describe("parsing", func() {
		It("is nil when the mission has no notification block", func() {
			_, f := writeFixture("config.hcl", missionWith("", ""))
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Notification).To(BeNil())
		})

		It("parses both channels with enabled defaulting to true", func() {
			block := `
  notification {
    gateway {
      events  = ["mission_failed"]
      channel = "#ops"
    }
    command_center { events = ["all"] }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, gatewayBlockHCL()))
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			n := cfg.Missions[0].Notification
			Expect(n).NotTo(BeNil())
			Expect(n.Gateway.Enabled).To(BeTrue())
			Expect(n.Gateway.Events).To(ConsistOf("mission_failed"))
			Expect(n.Gateway.Channel).To(Equal("#ops"))
			Expect(n.CommandCenter.Enabled).To(BeTrue())
			Expect(n.CommandCenter.Events).To(ConsistOf("all"))
		})

		It("expands \"all\" to the terminal events", func() {
			block := `
  notification {
    command_center { events = ["all"] }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			ch := cfg.Missions[0].Notification.CommandCenter
			Expect(ch.EffectiveEvents()).To(ConsistOf(
				config.NotifyMissionCompleted, config.NotifyMissionFailed))
			Expect(ch.WantsEvent(config.NotifyMissionFailed)).To(BeTrue())
		})

		It("honors enabled = false", func() {
			block := `
  notification {
    command_center {
      enabled = false
      events  = ["all"]
    }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			ch := cfg.Missions[0].Notification.CommandCenter
			Expect(ch.Enabled).To(BeFalse())
			Expect(ch.WantsEvent(config.NotifyMissionCompleted)).To(BeFalse())
		})
	})

	Describe("validation", func() {
		It("requires an explicit events list", func() {
			block := `
  notification {
    command_center { }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("'events' is required"))
		})

		It("rejects an unknown event name", func() {
			block := `
  notification {
    command_center { events = ["bogus_event"] }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid event"))
		})

		It("rejects 'channel' on the command_center channel", func() {
			block := `
  notification {
    command_center {
      events  = ["all"]
      channel = "#nope"
    }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only valid on the gateway channel"))
		})

		It("rejects an empty notification block", func() {
			block := `
  notification {
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least one of 'gateway' or 'command_center'"))
		})

		It("rejects a gateway channel when no gateway block is configured", func() {
			block := `
  notification {
    gateway { events = ["all"] }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, ""))
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no gateway block is configured"))
		})

		It("accepts a gateway channel when a gateway block exists", func() {
			block := `
  notification {
    gateway { events = ["all"] }
  }`
			_, f := writeFixture("config.hcl", missionWith(block, gatewayBlockHCL()))
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Notification.Gateway).NotTo(BeNil())
			Expect(cfg.Validate()).To(Succeed())
		})
	})
})

func gatewayBlockHCL() string {
	return `
gateway "slack" {
  version = "local"
  settings = {
    channel_id = "C123"
  }
}
`
}
