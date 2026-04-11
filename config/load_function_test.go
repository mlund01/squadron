package config_test

import (
	"os"
	"path/filepath"
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests exercise the `load()` HCL function through full config parsing
// so we verify both the function's behavior and that it's wired into the eval
// contexts where it's exposed (skill instructions, agent personality).
//
// Note: task objectives and similar deferred expressions preserve the raw HCL
// source and are re-evaluated at runtime without the load() function in
// scope, so those paths are deliberately not covered here.
var _ = Describe("load() HCL function", func() {

	// writeSkillFixture writes `skill.md` into dir, then writes a minimal
	// config that loads it via `load("./skill.md")` into a skill block.
	writeSkillConfig := func(dir, skillContent, skillPath string) string {
		hcl := fullBaseHCL() + `
skill "s" {
  description  = "desc"
  instructions = load("` + skillPath + `")
}
`
		Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
		return filepath.Join(dir, "config.hcl")
	}

	Context("path resolution", func() {
		It("loads ./relative paths from the HCL file's directory", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "skill.md"), []byte("adjacent content"), 0644)).To(Succeed())

			cfgPath := writeSkillConfig(dir, "adjacent content", "./skill.md")
			cfg, err := config.LoadFile(cfgPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Skills[0].Instructions).To(Equal("adjacent content"))
		})

		It("loads ../relative paths that climb out of the HCL directory", func() {
			root := GinkgoT().TempDir()
			sub := filepath.Join(root, "workflows")
			Expect(os.Mkdir(sub, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(root, "shared.md"), []byte("shared content"), 0644)).To(Succeed())

			cfgPath := writeSkillConfig(sub, "shared content", "../shared.md")
			cfg, err := config.LoadFile(cfgPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Skills[0].Instructions).To(Equal("shared content"))
		})

		It("loads bare paths from the process working directory", func() {
			origCWD, err := os.Getwd()
			Expect(err).NotTo(HaveOccurred())
			cwd := GinkgoT().TempDir()
			Expect(os.Chdir(cwd)).To(Succeed())
			DeferCleanup(func() { os.Chdir(origCWD) })

			Expect(os.WriteFile(filepath.Join(cwd, "guide.md"), []byte("from cwd"), 0644)).To(Succeed())

			// HCL lives in a different temp dir — the bare path must resolve
			// relative to CWD, not to the HCL's directory.
			hclDir := GinkgoT().TempDir()
			cfgPath := writeSkillConfig(hclDir, "from cwd", "guide.md")
			cfg, err := config.LoadFile(cfgPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Skills[0].Instructions).To(Equal("from cwd"))
		})

		It("rejects absolute paths starting with /", func() {
			dir := GinkgoT().TempDir()
			cfgPath := writeSkillConfig(dir, "", "/etc/hosts")
			_, err := config.LoadFile(cfgPath)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("absolute paths starting with '/' are not allowed"))
		})

		It("returns a clear error when the referenced file is missing", func() {
			dir := GinkgoT().TempDir()
			cfgPath := writeSkillConfig(dir, "", "./no_such_file.md")
			_, err := config.LoadFile(cfgPath)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no_such_file.md"))
		})
	})

	Context("file type allowlist", func() {
		DescribeTable("accepts allowed extensions",
			func(filename, content string) {
				dir := GinkgoT().TempDir()
				Expect(os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644)).To(Succeed())

				cfgPath := writeSkillConfig(dir, content, "./"+filename)
				cfg, err := config.LoadFile(cfgPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Skills[0].Instructions).To(Equal(content))
			},
			Entry(".md", "notes.md", "markdown content"),
			Entry(".txt", "notes.txt", "text content"),
		)

		DescribeTable("rejects disallowed extensions",
			func(filename string) {
				dir := GinkgoT().TempDir()
				Expect(os.WriteFile(filepath.Join(dir, filename), []byte("x"), 0644)).To(Succeed())

				cfgPath := writeSkillConfig(dir, "x", "./"+filename)
				_, err := config.LoadFile(cfgPath)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("only .md and .txt files are supported"))
			},
			Entry(".json", "data.json"),
			Entry(".yaml", "data.yaml"),
			Entry(".hcl", "data.hcl"),
			Entry("no extension", "README"),
		)
	})

	Context("usage contexts", func() {
		It("loads into skill instructions", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "skill.md"), []byte("skill body"), 0644)).To(Succeed())
			cfgPath := writeSkillConfig(dir, "skill body", "./skill.md")
			cfg, err := config.LoadFile(cfgPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Skills).To(HaveLen(1))
			Expect(cfg.Skills[0].Instructions).To(Equal("skill body"))
		})

		It("loads into agent personality", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "persona.md"), []byte("stoic"), 0644)).To(Succeed())
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "custom" {
  model       = models.anthropic.claude_sonnet_4
  personality = load("./persona.md")
  role        = "worker"
  tools       = [builtins.http.get]
}
`
			Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
			cfg, err := config.LoadFile(filepath.Join(dir, "config.hcl"))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].Personality).To(Equal("stoic"))
		})

		It("loads into agent-scoped skill instructions", func() {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "helper.md"), []byte("helper body"), 0644)).To(Succeed())
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "custom" {
  model       = models.anthropic.claude_sonnet_4
  personality = "helpful"
  role        = "worker"

  skill "helper" {
    description  = "helper"
    instructions = load("./helper.md")
  }
}
`
			Expect(os.WriteFile(filepath.Join(dir, "config.hcl"), []byte(hcl), 0644)).To(Succeed())
			cfg, err := config.LoadFile(filepath.Join(dir, "config.hcl"))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Agents[0].LocalSkills).To(HaveLen(1))
			Expect(cfg.Agents[0].LocalSkills[0].Instructions).To(Equal("helper body"))
		})
	})
})
