package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Storage Config", func() {

	Describe("Parsing", func() {
		It("parses ttl_days from the storage block", func() {
			hcl := `
storage {
  backend  = "sqlite"
  ttl_days = 30
}
`
			_, f := writeFixture("storage.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Storage).NotTo(BeNil())
			Expect(cfg.Storage.Backend).To(Equal("sqlite"))
			Expect(cfg.Storage.TTLDays).To(Equal(30))
		})

		It("defaults ttl_days to 0 when omitted", func() {
			hcl := `
storage {
  backend = "sqlite"
}
`
			_, f := writeFixture("storage.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Storage).NotTo(BeNil())
			Expect(cfg.Storage.TTLDays).To(Equal(0))
		})

		It("defaults ttl_days to 0 when the storage block is omitted", func() {
			_, f := writeFixture("config.hcl", `variable "x" { default = "val" }`)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Storage).NotTo(BeNil())
			Expect(cfg.Storage.TTLDays).To(Equal(0))
		})
	})

	Describe("Validation", func() {
		It("rejects a negative ttl_days", func() {
			hcl := `
storage {
  backend  = "sqlite"
  ttl_days = -1
}
`
			_, f := writeFixture("storage.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			err = cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ttl_days"))
		})

		It("accepts ttl_days of 0", func() {
			hcl := `
storage {
  backend  = "sqlite"
  ttl_days = 0
}
`
			_, f := writeFixture("storage.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Validate()).To(Succeed())
		})

		It("accepts a positive ttl_days", func() {
			hcl := `
storage {
  backend  = "sqlite"
  ttl_days = 7
}
`
			_, f := writeFixture("storage.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Validate()).To(Succeed())
		})
	})
})
