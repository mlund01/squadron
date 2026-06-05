package config_test

import (
	"os"
	"path/filepath"
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Context blocks", func() {
	It("loads a context block and exposes it on Config", func() {
		dir := GinkgoT().TempDir()
		ctxDir := filepath.Join(dir, "kb")
		Expect(os.MkdirAll(ctxDir, 0755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(ctxDir, "doc.md"), []byte("hello"), 0644)).To(Succeed())

		// Path is relative to the config file (./kb), not the absolute
		// tempdir. Absolute paths are rejected by ResolveContextPath.
		hcl := minimalVarsHCL() + `
context "kb" {
  path        = "./kb"
  description = "knowledge base"
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())

		cfg, err := config.LoadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Contexts).To(HaveLen(1))
		Expect(cfg.Contexts[0].Name).To(Equal("kb"))
		Expect(cfg.Contexts[0].Path).To(Equal(ctxDir))
	})

	It("skips HCL files inside a context folder", func() {
		dir := GinkgoT().TempDir()
		ctxDir := filepath.Join(dir, "kb")
		Expect(os.MkdirAll(ctxDir, 0755)).To(Succeed())

		// A stray invalid .hcl file inside the context folder must be ignored —
		// the loader can't even parse this one, and that's fine.
		bogus := `model "ghost" { provider = "openai" api_key = "x" }`
		Expect(os.WriteFile(filepath.Join(ctxDir, "stray.hcl"), []byte(bogus), 0644)).To(Succeed())

		hcl := minimalVarsHCL() + `
context "kb" {
  path = "./kb"
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())

		cfg, err := config.LoadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		for _, m := range cfg.Models {
			Expect(m.Name).NotTo(Equal("ghost"))
		}
	})

	It("rejects a mission that references an unknown context", func() {
		hcl := fullBaseHCL() + `
mission "go" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents   = [agents.test_agent]
  contexts = ["does_not_exist"]

  task "t" { objective = "do" }
}
`
		_, f := writeFixture("config.hcl", hcl)
		_, err := config.LoadAndValidate(f)
		Expect(err).To(HaveOccurred())
	})

	It("allows mission and task to declare contexts via the contexts namespace", func() {
		dir := GinkgoT().TempDir()
		kbDir := filepath.Join(dir, "kb")
		Expect(os.MkdirAll(kbDir, 0755)).To(Succeed())

		_ = kbDir // path uses relative form; kbDir is exists-check setup
		hcl := fullBaseHCL() + `
context "kb" {
  path = "./kb"
}

mission "go" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents   = [agents.test_agent]
  contexts = [contexts.kb]

  task "t1" {
    objective = "do"
    contexts  = [contexts.kb]
  }
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
		cfg, err := config.LoadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Missions).To(HaveLen(1))
		Expect(cfg.Missions[0].Contexts).To(ConsistOf("kb"))
		Expect(cfg.Missions[0].Tasks).To(HaveLen(1))
		Expect(cfg.Missions[0].Tasks[0].Contexts).To(ConsistOf("kb"))
	})

	It("rejects a context whose path does not exist", func() {
		hcl := minimalVarsHCL() + `
context "nope" {
  path = "./does-not-exist-xyz"
}
`
		_, f := writeFixture("config.hcl", hcl)
		_, err := config.LoadFile(f)
		Expect(err).To(HaveOccurred())
	})

	It("rejects an absolute (root-anchored) context path", func() {
		hcl := minimalVarsHCL() + `
context "abs" {
  path = "/tmp/anything"
}
`
		_, f := writeFixture("config.hcl", hcl)
		_, err := config.LoadFile(f)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("absolute"))
	})

	It("anchors a bare path to the config directory", func() {
		dir := GinkgoT().TempDir()
		// Folder lives directly next to the config file.
		Expect(os.MkdirAll(filepath.Join(dir, "side_ctx"), 0755)).To(Succeed())

		hcl := minimalVarsHCL() + `
context "sib" {
  path = "side_ctx"
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
		cfg, err := config.LoadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Contexts).To(HaveLen(1))
		Expect(cfg.Contexts[0].Path).To(Equal(filepath.Join(dir, "side_ctx")))
	})

	It("supports the @/ project-root marker", func() {
		dir := GinkgoT().TempDir()
		// Nested layout: configs/main.hcl with @/data/kb at project root.
		Expect(os.MkdirAll(filepath.Join(dir, "data", "kb"), 0755)).To(Succeed())

		hcl := minimalVarsHCL() + `
context "kb" {
  path = "@/data/kb"
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
		cfg, err := config.LoadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Contexts).To(HaveLen(1))
		Expect(cfg.Contexts[0].Path).To(Equal(filepath.Join(dir, "data", "kb")))
	})

	It("rejects a context path that resolves to the project root itself", func() {
		dir := GinkgoT().TempDir()

		hcl := minimalVarsHCL() + `
context "whole_project" {
  path = "@/"
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
		_, err := config.LoadDir(dir)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("resolves to the project root"))
	})

	It("rejects a context path that escapes the project root via ..", func() {
		dir := GinkgoT().TempDir()
		// The escape target literally exists in the parent of dir, but it's
		// outside the project — the loader must still reject the load.
		parent := filepath.Dir(dir)
		Expect(os.MkdirAll(filepath.Join(parent, "outside"), 0755)).To(Succeed())

		hcl := minimalVarsHCL() + `
context "leak" {
  path = "../outside"
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
		_, err := config.LoadDir(dir)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("escapes the project root"))
	})
})
