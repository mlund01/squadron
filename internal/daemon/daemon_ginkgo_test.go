package daemon

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDaemonSpecs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Daemon Suite")
}

var _ = Describe("ClearPid", func() {
	It("removes the PID file", func() {
		dir := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(dir, ".squadron"), 0755)).To(Succeed())
		Expect(os.WriteFile(PidFilePath(dir), []byte("12345"), 0644)).To(Succeed())

		ClearPid(dir)

		_, err := os.Stat(PidFilePath(dir))
		Expect(os.IsNotExist(err)).To(BeTrue(), "PID file should be gone after ClearPid")
	})

	It("is a no-op when no PID file exists", func() {
		Expect(func() { ClearPid(GinkgoT().TempDir()) }).NotTo(Panic())
	})
})

var _ = Describe("Reload", func() {
	Context("when the PID file is missing", func() {
		It("returns an error", func() {
			_, err := Reload(GinkgoT().TempDir())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("when the PID file is malformed", func() {
		It("returns an error", func() {
			dir := GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(dir, ".squadron"), 0755)).To(Succeed())
			Expect(os.WriteFile(PidFilePath(dir), []byte("not-a-pid"), 0644)).To(Succeed())

			_, err := Reload(dir)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("when the target process does not exist", func() {
		It("returns an error", func() {
			dir := GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(dir, ".squadron"), 0755)).To(Succeed())
			// A high PID that almost certainly doesn't exist.
			Expect(os.WriteFile(PidFilePath(dir), []byte("999999"), 0644)).To(Succeed())

			_, err := Reload(dir)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("when the target process is alive", func() {
		It("delivers SIGHUP to the recorded PID", func() {
			dir := GinkgoT().TempDir()
			Expect(os.MkdirAll(filepath.Join(dir, ".squadron"), 0755)).To(Succeed())

			pid := os.Getpid()
			Expect(os.WriteFile(PidFilePath(dir), []byte(fmt.Sprintf("%d", pid)), 0644)).To(Succeed())

			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGHUP)
			DeferCleanup(func() { signal.Stop(sigs) })

			gotPID, err := Reload(dir)
			Expect(err).NotTo(HaveOccurred())
			Expect(gotPID).To(Equal(pid))

			Eventually(sigs, 2*time.Second).Should(Receive())
		})
	})
})
