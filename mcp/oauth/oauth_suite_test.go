package oauth_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/config/vault"
)

var origSquadronHome string

func TestOAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MCP OAuth Suite")
}

var _ = BeforeSuite(func() {
	origSquadronHome = os.Getenv("SQUADRON_HOME")
})

var _ = AfterSuite(func() {
	if origSquadronHome != "" {
		os.Setenv("SQUADRON_HOME", origSquadronHome)
	} else {
		os.Unsetenv("SQUADRON_HOME")
	}
})

// initTestVault points SQUADRON_HOME at a temp dir and bootstraps an empty
// encrypted vault so kvstore operations work. Uses the fallback passphrase
// to avoid keyring interaction.
func initTestVault() string {
	dir := GinkgoT().TempDir()
	os.Setenv("SQUADRON_HOME", dir)

	v := vault.Open(dir + "/vars.vault")
	Expect(v.Save([]byte(vault.FallbackPassphrase), map[string]string{})).To(Succeed())
	return dir
}
