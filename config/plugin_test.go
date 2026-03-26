package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plugin", func() {

	Describe("parsing", func() {
		It("returns error when plugin binary is not found", func() {
			hcl := minimalVarsHCL() + `
plugin "myplugin" {
  source  = "github.com/example/myplugin"
  version = "v1.0.0"
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("myplugin"))
		})

		It("returns error when configured plugin binary is not found", func() {
			hcl := minimalVarsHCL() + `
plugin "configured" {
  source  = "github.com/example/configured"
  version = "local"

  settings {
    headless = false
    port     = 8080
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("configured"))
		})
	})

	Describe("Validate", func() {
		Context("reserved names", func() {
			DescribeTable("rejects reserved plugin namespace",
				func(name string) {
					p := config.Plugin{Name: name, Source: "github.com/x/y", Version: "v1.0.0"}
					err := p.Validate()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("reserved"))
				},
				Entry("http", "http"),
				Entry("utils", "utils"),
			)
		})

		Context("version format", func() {
			DescribeTable("accepts valid version strings",
				func(version string) {
					p := config.Plugin{Name: "myplugin", Source: "github.com/x/y", Version: version}
					Expect(p.Validate()).To(Succeed())
				},
				Entry("local", "local"),
				Entry("semver v-prefix", "v1.0.0"),
				Entry("semver no-prefix", "1.0.0"),
				Entry("semver pre-release", "v1.0.0-beta"),
				Entry("semver pre-release dot", "v1.0.0-rc.1"),
				Entry("semver build metadata", "v1.0.0+build.123"),
			)

			DescribeTable("rejects invalid version strings",
				func(version string) {
					p := config.Plugin{Name: "myplugin", Source: "github.com/x/y", Version: version}
					err := p.Validate()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("invalid version"))
				},
				Entry("plain text", "latest"),
				Entry("partial semver", "v1.0"),
				Entry("just major", "v1"),
			)
		})

		It("rejects empty name", func() {
			p := config.Plugin{Name: "", Source: "github.com/x/y", Version: "v1.0.0"}
			err := p.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("name is required"))
		})

		It("rejects empty source", func() {
			p := config.Plugin{Name: "p", Source: "", Version: "v1.0.0"}
			err := p.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("source is required"))
		})

		It("rejects empty version", func() {
			p := config.Plugin{Name: "p", Source: "github.com/x", Version: ""}
			err := p.Validate()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("version is required"))
		})
	})
})
