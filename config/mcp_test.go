package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MCP", func() {

	Describe("MCPServer.Validate", func() {

		Context("name", func() {
			It("rejects an empty name", func() {
				m := config.MCPServer{Command: "/bin/echo"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("name is required"))
			})

			DescribeTable("rejects reserved builtin namespaces",
				func(name string) {
					m := config.MCPServer{Name: name, Command: "/bin/echo"}
					err := m.Validate()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("reserved"))
				},
				Entry("http", "http"),
				Entry("dataset", "dataset"),
			)
		})

		Context("mode selection (command / url / source)", func() {
			It("rejects when none of command, url, source are set", func() {
				m := config.MCPServer{Name: "fs"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("one of command, url, or source is required"))
			})

			It("rejects when command and url are both set", func() {
				m := config.MCPServer{Name: "fs", Command: "/bin/x", URL: "https://example.com"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
			})

			It("rejects when command and source are both set", func() {
				m := config.MCPServer{Name: "fs", Command: "/bin/x", Source: "npm:foo", Version: "1.0.0"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
			})

			It("rejects when url and source are both set", func() {
				m := config.MCPServer{Name: "fs", URL: "https://example.com", Source: "npm:foo", Version: "1.0.0"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
			})

			It("accepts a bare command", func() {
				m := config.MCPServer{Name: "fs", Command: "/bin/echo"}
				Expect(m.Validate()).To(Succeed())
			})

			It("accepts an http url", func() {
				m := config.MCPServer{Name: "remote", URL: "https://example.com/mcp"}
				Expect(m.Validate()).To(Succeed())
			})
		})

		Context("transport-gated fields", func() {
			It("rejects env on http (url) servers", func() {
				m := config.MCPServer{
					Name: "remote",
					URL:  "https://example.com",
					Env:  map[string]string{"KEY": "val"},
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("env is not valid on http"))
			})

			It("rejects args on http (url) servers", func() {
				m := config.MCPServer{
					Name: "remote",
					URL:  "https://example.com",
					Args: []string{"--flag"},
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("args is not valid on http"))
			})

			It("rejects headers on stdio (command) servers", func() {
				m := config.MCPServer{
					Name:    "local",
					Command: "/bin/x",
					Headers: map[string]string{"Authorization": "Bearer x"},
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("headers is only valid on http"))
			})

			It("rejects headers on source-backed servers", func() {
				m := config.MCPServer{
					Name:    "fs",
					Source:  "npm:@modelcontextprotocol/server-filesystem",
					Version: "1.0.0",
					Headers: map[string]string{"X": "y"},
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("headers is only valid on http"))
			})

			It("accepts env and args on stdio command servers", func() {
				m := config.MCPServer{
					Name:    "local",
					Command: "/bin/x",
					Args:    []string{"--flag"},
					Env:     map[string]string{"KEY": "val"},
				}
				Expect(m.Validate()).To(Succeed())
			})

			It("accepts headers on http servers", func() {
				m := config.MCPServer{
					Name:    "remote",
					URL:     "https://example.com",
					Headers: map[string]string{"Authorization": "Bearer x"},
				}
				Expect(m.Validate()).To(Succeed())
			})
		})

		Context("version rules", func() {
			It("requires version when source is set", func() {
				m := config.MCPServer{Name: "fs", Source: "npm:pkg"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("version is required"))
			})

			It("rejects version without source on command servers", func() {
				m := config.MCPServer{Name: "fs", Command: "/bin/x", Version: "v1.0.0"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("version is only valid with source"))
			})

			It("rejects version without source on url servers", func() {
				m := config.MCPServer{Name: "remote", URL: "https://example.com", Version: "v1.0.0"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("version is only valid with source"))
			})
		})

		Context("source scheme", func() {
			It("accepts npm: sources", func() {
				m := config.MCPServer{
					Name:    "fs",
					Source:  "npm:@modelcontextprotocol/server-filesystem",
					Version: "2026.1.14",
				}
				Expect(m.Validate()).To(Succeed())
			})

			It("accepts github.com/owner/repo sources", func() {
				m := config.MCPServer{
					Name:    "custom",
					Source:  "github.com/owner/repo",
					Version: "v1.0.0",
				}
				Expect(m.Validate()).To(Succeed())
			})

			It("rejects unknown source schemes", func() {
				m := config.MCPServer{
					Name:    "fs",
					Source:  "gitlab.com/owner/repo",
					Version: "v1.0.0",
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("source must start with 'npm:' or 'github.com/'"))
			})

			It("rejects empty npm package name", func() {
				m := config.MCPServer{Name: "fs", Source: "npm:", Version: "1.0.0"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("requires a package name"))
			})

			DescribeTable("rejects malformed github source",
				func(source string) {
					m := config.MCPServer{Name: "x", Source: source, Version: "v1.0.0"}
					err := m.Validate()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("github.com/owner/repo"))
				},
				Entry("missing repo", "github.com/owner"),
				Entry("empty owner", "github.com//repo"),
				Entry("empty repo", "github.com/owner/"),
				Entry("too many parts", "github.com/owner/repo/extra"),
			)
		})

		Context("entry field", func() {
			It("accepts entry with github source", func() {
				m := config.MCPServer{
					Name:    "custom",
					Source:  "github.com/owner/repo",
					Version: "v1.0.0",
					Entry:   "bin/server",
				}
				Expect(m.Validate()).To(Succeed())
			})

			It("rejects entry with npm source", func() {
				m := config.MCPServer{
					Name:    "fs",
					Source:  "npm:pkg",
					Version: "1.0.0",
					Entry:   "index.js",
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("entry is only valid with github sources"))
			})

			It("rejects entry without source", func() {
				m := config.MCPServer{Name: "local", Command: "/bin/x", Entry: "bin/server"}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("entry is only valid"))
			})
		})

		Context("OAuth client credentials", func() {
			It("accepts client_id on http servers", func() {
				m := config.MCPServer{
					Name:     "oauth",
					URL:      "https://example.com/mcp",
					ClientID: "my-client",
				}
				Expect(m.Validate()).To(Succeed())
			})

			It("accepts client_id and client_secret on http servers", func() {
				m := config.MCPServer{
					Name:         "oauth",
					URL:          "https://example.com/mcp",
					ClientID:     "my-client",
					ClientSecret: "my-secret",
				}
				Expect(m.Validate()).To(Succeed())
			})

			It("rejects client_id on command servers", func() {
				m := config.MCPServer{
					Name:     "local",
					Command:  "/bin/x",
					ClientID: "my-client",
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("client_id is only valid on http"))
			})

			It("rejects client_id on source-backed servers", func() {
				m := config.MCPServer{
					Name:     "fs",
					Source:   "npm:@modelcontextprotocol/server-filesystem",
					Version:  "1.0.0",
					ClientID: "my-client",
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("client_id is only valid on http"))
			})

			It("rejects client_secret without client_id", func() {
				m := config.MCPServer{
					Name:         "oauth",
					URL:          "https://example.com/mcp",
					ClientSecret: "orphan-secret",
				}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("client_secret requires client_id"))
			})
		})
	})

	Describe("MCPHostConfig", func() {
		Describe("Defaults", func() {
			It("fills in default port when unset", func() {
				c := &config.MCPHostConfig{}
				c.Defaults()
				Expect(c.Port).To(Equal(8090))
			})

			It("preserves an explicit port", func() {
				c := &config.MCPHostConfig{Port: 9000}
				c.Defaults()
				Expect(c.Port).To(Equal(9000))
			})
		})

		Describe("Validate", func() {
			It("accepts a port in the valid range", func() {
				c := &config.MCPHostConfig{Port: 8090}
				Expect(c.Validate()).To(Succeed())
			})

			It("rejects ports below 1024", func() {
				c := &config.MCPHostConfig{Port: 80}
				err := c.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("between 1024 and 65535"))
			})

			It("rejects ports above 65535", func() {
				c := &config.MCPHostConfig{Port: 70000}
				err := c.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("between 1024 and 65535"))
			})
		})
	})

	Describe("HCL parsing", func() {
		// Successful mcp consumer parsing is covered by integration — it spawns
		// a real subprocess, which belongs in a higher-level suite. These tests
		// cover the HCL wiring: host blocks parse and validation errors on
		// consumer blocks surface through the loader.

		It("parses an mcp_host block", func() {
			hcl := minimalVarsHCL() + `
mcp_host {
  enabled = true
  port    = 9000
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.MCPHost).NotTo(BeNil())
			Expect(cfg.MCPHost.Enabled).To(BeTrue())
			Expect(cfg.MCPHost.Port).To(Equal(9000))
		})

		It("surfaces validation errors on malformed mcp blocks", func() {
			hcl := minimalVarsHCL() + `
mcp "broken" {
  command = "/bin/x"
  url     = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
		})

		It("surfaces validation errors on missing version", func() {
			hcl := minimalVarsHCL() + `
mcp "bad" {
  source = "npm:@some/package"
}
`
			_, f := writeFixture("config.hcl", hcl)
			_, err := config.LoadFile(f)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("version is required"))
		})
	})
})
