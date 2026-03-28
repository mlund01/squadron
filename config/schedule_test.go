package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Mission Schedules & Triggers", func() {

	Describe("Schedule parsing", func() {
		It("parses an 'at' schedule with weekdays", func() {
			hcl := fullBaseHCL() + `
mission "daily" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    at       = ["09:00"]
    weekdays = ["mon", "tue", "wed", "thu", "fri"]
    timezone = "America/Chicago"
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("sched.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions).To(HaveLen(1))
			Expect(cfg.Missions[0].Schedules).To(HaveLen(1))
			sched := cfg.Missions[0].Schedules[0]
			Expect(sched.At).To(ConsistOf("09:00"))
			Expect(sched.Weekdays).To(ConsistOf("mon", "tue", "wed", "thu", "fri"))
			Expect(sched.Timezone).To(Equal("America/Chicago"))
		})

		It("parses an 'every' schedule", func() {
			hcl := fullBaseHCL() + `
mission "frequent" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    every = "15m"
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("every-sched.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			sched := cfg.Missions[0].Schedules[0]
			Expect(sched.Every).To(Equal("15m"))
		})

		It("parses a cron schedule", func() {
			hcl := fullBaseHCL() + `
mission "cleanup" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    cron     = "0 0 * * sun"
    timezone = "UTC"
  }

  task "clean" { objective = "Cleanup" }
}
`
			_, f := writeFixture("cron.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			sched := cfg.Missions[0].Schedules[0]
			Expect(sched.Cron).To(Equal("0 0 * * sun"))
			Expect(sched.Timezone).To(Equal("UTC"))
		})

		It("parses multiple schedules per mission", func() {
			hcl := fullBaseHCL() + `
mission "multi" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule { every = "1h" }
  schedule { cron = "0 9 * * *" }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("multi-sched.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Schedules).To(HaveLen(2))
		})

		It("parses schedule with inputs", func() {
			hcl := fullBaseHCL() + `
mission "with_inputs" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    at = ["09:00"]
    inputs = {
      report_type = "daily"
      recipient   = "team@example.com"
    }
  }

  input "report_type" {
    type = "string"
  }

  input "recipient" {
    type = "string"
  }

  task "work" { objective = "Do work for ${inputs.report_type}" }
}
`
			_, f := writeFixture("sched-inputs.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			sched := cfg.Missions[0].Schedules[0]
			Expect(sched.Inputs).To(HaveLen(2))
			Expect(sched.Inputs["report_type"]).To(Equal("daily"))
			Expect(sched.Inputs["recipient"]).To(Equal("team@example.com"))
		})

		It("allows missions with no schedules", func() {
			hcl := fullBaseHCL() + `
mission "manual" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("no-sched.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Schedules).To(BeEmpty())
		})
	})

	Describe("Schedule validation", func() {
		It("rejects schedule with both every and cron", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    every = "1h"
    cron  = "0 * * * *"
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("both.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly one of"))
		})

		It("rejects schedule with no mode set", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    timezone = "UTC"
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("neither.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly one of"))
		})

		It("rejects every duration less than 1m", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule { every = "30s" }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("short.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 1m"))
		})

		It("rejects invalid at format", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    at = ["9am"]
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("bad-at.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("HH:MM"))
		})

		It("rejects invalid weekday", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    every    = "1h"
    weekdays = ["monday"]
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("bad-weekday.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be mon-sun"))
		})

		It("rejects every that doesn't divide evenly", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule { every = "7m" }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("bad-every.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("divide evenly"))
		})

		It("rejects at combined with every", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    every = "1h"
    at    = ["09:00"]
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("at-every.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly one of"))
		})

		It("rejects invalid cron expression", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule { cron = "not a cron" }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("bad-cron.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid cron"))
		})

		It("rejects invalid timezone", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    every    = "1h"
    timezone = "Not/A/Timezone"
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("bad-tz.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid timezone"))
		})

		It("rejects at/weekdays with cron", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  schedule {
    cron     = "0 9 * * *"
    weekdays = ["mon"]
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("cron-weekdays.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cannot be used with 'cron'"))
		})
	})

	Describe("Trigger parsing", func() {
		It("parses a trigger block with explicit webhook_path", func() {
			hcl := fullBaseHCL() + `
mission "hook" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  trigger {
    webhook_path = "/api/ingest"
    secret       = vars.test_api_key
  }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("trigger.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Trigger).NotTo(BeNil())
			Expect(cfg.Missions[0].Trigger.WebhookPath).To(Equal("/api/ingest"))
			Expect(cfg.Missions[0].Trigger.Secret).To(Equal("test-key-123"))
		})

		It("defaults webhook_path to mission name", func() {
			hcl := fullBaseHCL() + `
mission "my_mission" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  trigger {}

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("trigger-default.hcl", hcl)
			cfg, err := config.LoadAndValidate(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Trigger.WebhookPath).To(Equal("/my_mission"))
		})

		It("rejects duplicate webhook paths", func() {
			hcl := fullBaseHCL() + `
mission "a" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  trigger { webhook_path = "/shared" }
  task "work" { objective = "Do work" }
}

mission "b" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  trigger { webhook_path = "/shared" }
  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("dup-webhook.hcl", hcl)
			_, err := config.LoadAndValidate(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("conflicts with"))
		})

		It("rejects multiple trigger blocks in one mission", func() {
			hcl := fullBaseHCL() + `
mission "bad" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]

  trigger { webhook_path = "/one" }
  trigger { webhook_path = "/two" }

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("multi-trigger.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one trigger"))
		})
	})

	Describe("max_parallel", func() {
		It("defaults to 3", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("default-parallel.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].MaxParallel).To(Equal(3))
		})

		It("parses custom max_parallel", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents       = [agents.test_agent]
  max_parallel = 1

  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("custom-parallel.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].MaxParallel).To(Equal(1))
		})
	})
})
