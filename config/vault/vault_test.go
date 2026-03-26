package vault_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/config/vault"
)

var _ = Describe("Vault", func() {

	Describe("Save and Load", func() {
		It("round-trips variables through encrypt/decrypt", func() {
			path := filepath.Join(GinkgoT().TempDir(), "test.vault")
			v := vault.Open(path)
			passphrase := []byte("test-passphrase-12345")

			vars := map[string]string{
				"api_key":   "sk-12345",
				"db_pass":   "hunter2",
				"empty_val": "",
			}

			Expect(v.Save(passphrase, vars)).To(Succeed())
			Expect(v.Exists()).To(BeTrue())

			loaded, err := v.Load(passphrase)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(HaveLen(len(vars)))
			for k, want := range vars {
				Expect(loaded).To(HaveKeyWithValue(k, want))
			}
		})

		It("handles an empty map", func() {
			path := filepath.Join(GinkgoT().TempDir(), "test.vault")
			v := vault.Open(path)
			passphrase := []byte("test-passphrase")

			Expect(v.Save(passphrase, map[string]string{})).To(Succeed())

			loaded, err := v.Load(passphrase)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(BeEmpty())
		})

		It("produces different ciphertexts for identical data", func() {
			path := filepath.Join(GinkgoT().TempDir(), "test.vault")
			v := vault.Open(path)
			passphrase := []byte("test-passphrase")
			vars := map[string]string{"key": "value"}

			Expect(v.Save(passphrase, vars)).To(Succeed())
			data1, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())

			Expect(v.Save(passphrase, vars)).To(Succeed())
			data2, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())

			Expect(data1).NotTo(Equal(data2))

			loaded, err := v.Load(passphrase)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).To(HaveKeyWithValue("key", "value"))
		})
	})

	Describe("error handling", func() {
		It("returns ErrWrongPassphrase for incorrect passphrase", func() {
			path := filepath.Join(GinkgoT().TempDir(), "test.vault")
			v := vault.Open(path)

			Expect(v.Save([]byte("correct-passphrase"), map[string]string{"key": "val"})).To(Succeed())

			_, err := v.Load([]byte("wrong-passphrase"))
			Expect(err).To(MatchError(vault.ErrWrongPassphrase))
		})

		It("returns an error for corrupt files", func() {
			cases := []struct {
				name string
				data []byte
			}{
				{"empty", []byte{}},
				{"too short", []byte("SHORT")},
				{"bad magic", []byte("NOTVALID" + string([]byte{0x01}) + "saltsaltsaltsalt" + "noncenonceno" + "ciphertext")},
			}

			for _, tc := range cases {
				path := filepath.Join(GinkgoT().TempDir(), "test.vault")
				Expect(os.WriteFile(path, tc.data, 0600)).To(Succeed())
				v := vault.Open(path)

				_, err := v.Load([]byte("passphrase"))
				Expect(err).To(HaveOccurred(), "expected error for %s", tc.name)
			}
		})
	})

	Describe("Exists", func() {
		It("returns false for a nonexistent vault", func() {
			path := filepath.Join(GinkgoT().TempDir(), "nonexistent.vault")
			v := vault.Open(path)
			Expect(v.Exists()).To(BeFalse())
		})
	})

	Describe("GeneratePassphrase", func() {
		It("generates 32-byte passphrases", func() {
			p, err := vault.GeneratePassphrase()
			Expect(err).NotTo(HaveOccurred())
			Expect(p).To(HaveLen(32))
		})

		It("generates unique passphrases", func() {
			p1, err := vault.GeneratePassphrase()
			Expect(err).NotTo(HaveOccurred())

			p2, err := vault.GeneratePassphrase()
			Expect(err).NotTo(HaveOccurred())

			Expect(p1).NotTo(Equal(p2))
		})
	})

	Describe("ZeroBytes", func() {
		It("zeroes all bytes in the slice", func() {
			data := []byte("secret-data")
			vault.ZeroBytes(data)
			for i, b := range data {
				Expect(b).To(BeZero(), "byte %d not zeroed", i)
			}
		})
	})
})
