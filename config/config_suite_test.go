package config_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}

// writeFixture writes an HCL file to a temp directory and returns the dir and file paths.
func writeFixture(filename, content string) (dir string, filePath string) {
	dir = GinkgoT().TempDir()
	filePath = filepath.Join(dir, filename)
	err := os.WriteFile(filePath, []byte(content), 0644)
	Expect(err).NotTo(HaveOccurred())
	return dir, filePath
}

// writeFixtures writes multiple HCL files to a single temp directory and returns the dir path.
func writeFixtures(files map[string]string) string {
	dir := GinkgoT().TempDir()
	for filename, content := range files {
		err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644)
		Expect(err).NotTo(HaveOccurred())
	}
	return dir
}

// minimalVarsHCL returns HCL for a variable with a default (avoids needing ~/.squadron/vars.txt).
func minimalVarsHCL() string {
	return `
variable "test_api_key" {
  default = "test-key-123"
}
`
}

// minimalModelHCL returns HCL for a valid anthropic model config.
func minimalModelHCL() string {
	return `
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.test_api_key
}
`
}

// minimalAgentHCL returns HCL for a valid agent with an internal tool.
func minimalAgentHCL() string {
	return `
agent "test_agent" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful"
  role        = "Test agent"
  tools       = [plugins.bash.bash]
}
`
}

// fullBaseHCL returns vars + model + agent needed for mission tests.
func fullBaseHCL() string {
	return minimalVarsHCL() + minimalModelHCL() + minimalAgentHCL()
}
