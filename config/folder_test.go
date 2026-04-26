package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Folders", func() {

	Describe("shared_folder", func() {
		It("parses a shared_folder block", func() {
			hcl := fullBaseHCL() + `
shared_folder "research" {
  path        = "./data"
  description = "Research docs"
  editable    = true
}
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  folders   = [shared_folders.research]
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.SharedFolders).To(HaveLen(1))
			Expect(cfg.SharedFolders[0].Name).To(Equal("research"))
			Expect(cfg.SharedFolders[0].Editable).To(BeTrue())
			Expect(cfg.Missions[0].Folders).To(ConsistOf("research"))
		})

		It("rejects the reserved name 'mission'", func() {
			sf := config.SharedFolder{Name: "mission", Path: "./x"}
			err := sf.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects the reserved name 'run'", func() {
			sf := config.SharedFolder{Name: "run", Path: "./x"}
			err := sf.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reserved"))
		})

		It("rejects an empty path", func() {
			sf := config.SharedFolder{Name: "ok", Path: ""}
			err := sf.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("path is required"))
		})
	})

	Describe("mission folder block", func() {
		It("parses a dedicated folder block", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  folder {
    path        = "./persistent"
    description = "Persistent"
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Folder).NotTo(BeNil())
			Expect(cfg.Missions[0].Folder.Path).To(Equal("./persistent"))
			Expect(cfg.Missions[0].Folder.Description).To(Equal("Persistent"))
		})

		It("rejects multiple folder blocks on the same mission", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  folder { path = "./a" }
  folder { path = "./b" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one folder block allowed"))
		})
	})

	Describe("mission run_folder block", func() {
		It("parses a run_folder with defaults", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  run_folder {
    description = "Per-run scratch"
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].RunFolder).NotTo(BeNil())
			Expect(cfg.Missions[0].RunFolder.Base).To(Equal(""))
			Expect(cfg.Missions[0].RunFolder.Description).To(Equal("Per-run scratch"))
			// Cleanup omitted → Validate() filled in the default
			Expect(cfg.Missions[0].RunFolder.Cleanup).NotTo(BeNil())
			Expect(*cfg.Missions[0].RunFolder.Cleanup).To(Equal(config.DefaultRunFolderCleanupDays))
		})

		It("parses a run_folder with custom base and cleanup", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  run_folder {
    base    = "./custom_runs"
    cleanup = 14
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].RunFolder.Base).To(Equal("./custom_runs"))
			Expect(*cfg.Missions[0].RunFolder.Cleanup).To(Equal(14))
		})

		It("preserves an explicit cleanup of zero (never delete)", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  run_folder {
    cleanup = 0
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].RunFolder.Cleanup).NotTo(BeNil())
			Expect(*cfg.Missions[0].RunFolder.Cleanup).To(Equal(0))
		})

		It("rejects multiple run_folder blocks", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  run_folder { base = "./a" }
  run_folder { base = "./b" }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("only one run_folder block allowed"))
		})

		It("rejects a negative cleanup value", func() {
			neg := -1
			rf := config.MissionRunFolder{Cleanup: &neg}
			err := rf.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cleanup"))
		})

		It("allows cleanup of zero (never)", func() {
			zero := 0
			rf := config.MissionRunFolder{Cleanup: &zero}
			Expect(rf.Validate()).To(Succeed())
			Expect(*rf.Cleanup).To(Equal(0))
		})

		It("Validate accepts an unset Cleanup (parser fills the default)", func() {
			rf := config.MissionRunFolder{}
			Expect(rf.Validate()).To(Succeed())
		})

		It("allows both folder and run_folder on the same mission", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.test_agent]
  folder {
    path = "./persist"
  }
  run_folder {
    base    = "./runs"
    cleanup = 7
  }
  task "t" { objective = "go" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Folder).NotTo(BeNil())
			Expect(cfg.Missions[0].RunFolder).NotTo(BeNil())
		})
	})
})
