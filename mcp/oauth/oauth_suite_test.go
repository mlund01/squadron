package oauth_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/config/vault"
	"squadron/internal/paths"
)

var origDir string

func TestOAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MCP OAuth Suite")
}

var _ = BeforeSuite(func() {
	var err error
	origDir, err = os.Getwd()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	Expect(os.Chdir(origDir)).To(Succeed())
	paths.ResetHome()
})

// initTestVault changes into a temp dir with a .squadron/ subdirectory and
// bootstraps an empty encrypted vault so kvstore operations work. Uses the
// fallback passphrase to avoid keyring interaction.
func initTestVault() string {
	dir := GinkgoT().TempDir()
	sqDir := filepath.Join(dir, ".squadron")
	Expect(os.MkdirAll(sqDir, 0700)).To(Succeed())
	Expect(os.Chdir(dir)).To(Succeed())
	paths.ResetHome()

	v := vault.Open(filepath.Join(sqDir, "vars.vault"))
	Expect(v.Save([]byte(vault.FallbackPassphrase), map[string]string{})).To(Succeed())
	return dir
}
