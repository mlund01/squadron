package config_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var origSquadronHome string

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config Suite")
}

var _ = BeforeSuite(func() {
	// Isolate all config tests from the real vault/keychain
	origSquadronHome = os.Getenv("SQUADRON_HOME")
	tmpHome, err := os.MkdirTemp("", "squadron-test-*")
	Expect(err).NotTo(HaveOccurred())
	os.Setenv("SQUADRON_HOME", tmpHome)
})

var _ = AfterSuite(func() {
	if origSquadronHome != "" {
		os.Setenv("SQUADRON_HOME", origSquadronHome)
	} else {
		os.Unsetenv("SQUADRON_HOME")
	}
})

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

// minimalStorageHCL is appended to inline HCL fixtures that don't use minimalVarsHCL,
// so that LoadFile always receives a storage block.
const minimalStorageHCL = "\nstorage {\n  backend = \"sqlite\"\n}\n"

// minimalVarsHCL returns HCL for a variable with a default + storage block (the universal base for all tests).
func minimalVarsHCL() string {
	return `
variable "test_api_key" {
  default = "test-key-123"
}
storage {
  backend = "sqlite"
}
`
}

// minimalModelHCL returns HCL for a valid anthropic model config.
func minimalModelHCL() string {
	return `
model "anthropic" {
  provider       = "anthropic"
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
  tools       = [builtins.http.get]
}
`
}

// fullBaseHCL returns vars + model + agent needed for mission tests.
func fullBaseHCL() string {
	return minimalVarsHCL() + minimalModelHCL() + minimalAgentHCL()
}
