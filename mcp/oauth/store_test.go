package oauth_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mark3labs/mcp-go/client/transport"

	"squadron/mcp/oauth"
)

var _ = Describe("VaultTokenStore", func() {
	BeforeEach(func() { initTestVault() })

	It("round-trips a token through Save and Get", func() {
		store := oauth.NewVaultTokenStore("test-server")

		tok := &transport.Token{
			AccessToken:  "access-123",
			TokenType:    "Bearer",
			RefreshToken: "refresh-456",
			ExpiresIn:    3600,
		}
		Expect(store.SaveToken(context.Background(), tok)).To(Succeed())

		got, err := store.GetToken(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AccessToken).To(Equal("access-123"))
		Expect(got.RefreshToken).To(Equal("refresh-456"))
		Expect(got.ExpiresAt).NotTo(BeZero(), "SaveToken should compute ExpiresAt from ExpiresIn")
	})

	It("returns ErrNoToken when no token is stored", func() {
		store := oauth.NewVaultTokenStore("nonexistent")
		_, err := store.GetToken(context.Background())
		Expect(err).To(MatchError(transport.ErrNoToken))
	})

	It("rejects a nil token on Save", func() {
		store := oauth.NewVaultTokenStore("test-server")
		Expect(store.SaveToken(context.Background(), nil)).To(HaveOccurred())
	})

	It("preserves ExpiresAt when already set", func() {
		store := oauth.NewVaultTokenStore("test-server")
		fixed := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		tok := &transport.Token{
			AccessToken: "a",
			ExpiresAt:   fixed,
			ExpiresIn:   9999,
		}
		Expect(store.SaveToken(context.Background(), tok)).To(Succeed())

		got, err := store.GetToken(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ExpiresAt).To(BeTemporally("==", fixed))
	})

	It("respects context cancellation on GetToken", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		store := oauth.NewVaultTokenStore("test-server")
		_, err := store.GetToken(ctx)
		Expect(err).To(MatchError(context.Canceled))
	})
})

var _ = Describe("DeleteToken", func() {
	BeforeEach(func() { initTestVault() })

	It("removes a stored token", func() {
		store := oauth.NewVaultTokenStore("del-test")
		Expect(store.SaveToken(context.Background(), &transport.Token{AccessToken: "x"})).To(Succeed())
		Expect(oauth.HasToken("del-test")).To(BeTrue())

		Expect(oauth.DeleteToken("del-test")).To(Succeed())
		Expect(oauth.HasToken("del-test")).To(BeFalse())
	})

	It("is idempotent — deleting a missing token succeeds", func() {
		Expect(oauth.DeleteToken("never-existed")).To(Succeed())
	})
})

var _ = Describe("ClientCredentials", func() {
	BeforeEach(func() { initTestVault() })

	It("round-trips Save and Load", func() {
		creds := oauth.ClientCredentials{ClientID: "cid-1", ClientSecret: "sec-1"}
		Expect(oauth.SaveClientCredentials("cred-test", creds)).To(Succeed())

		got, err := oauth.LoadClientCredentials("cred-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got.ClientID).To(Equal("cid-1"))
		Expect(got.ClientSecret).To(Equal("sec-1"))
	})

	It("returns (nil, nil) when no credentials are stored", func() {
		got, err := oauth.LoadClientCredentials("missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})
})

var _ = Describe("HasToken", func() {
	BeforeEach(func() { initTestVault() })

	It("returns false when no token exists", func() {
		Expect(oauth.HasToken("nope")).To(BeFalse())
	})

	It("returns true after a token is saved", func() {
		store := oauth.NewVaultTokenStore("ht-test")
		Expect(store.SaveToken(context.Background(), &transport.Token{AccessToken: "x"})).To(Succeed())
		Expect(oauth.HasToken("ht-test")).To(BeTrue())
	})
})

var _ = Describe("VaultSnapshot", func() {
	BeforeEach(func() { initTestVault() })

	It("sees tokens stored before the snapshot was taken", func() {
		store := oauth.NewVaultTokenStore("snap-test")
		Expect(store.SaveToken(context.Background(), &transport.Token{
			AccessToken: "snap-tok",
			ExpiresAt:   time.Now().Add(time.Hour),
		})).To(Succeed())

		snap, err := oauth.LoadVaultSnapshot()
		Expect(err).NotTo(HaveOccurred())
		Expect(snap.HasToken("snap-test")).To(BeTrue())

		tok, err := snap.Token("snap-test")
		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).To(Equal("snap-tok"))
	})

	It("does not see tokens stored after the snapshot", func() {
		snap, err := oauth.LoadVaultSnapshot()
		Expect(err).NotTo(HaveOccurred())

		store := oauth.NewVaultTokenStore("late-write")
		Expect(store.SaveToken(context.Background(), &transport.Token{AccessToken: "new"})).To(Succeed())

		Expect(snap.HasToken("late-write")).To(BeFalse())
	})

	It("returns ErrNoToken for a missing server", func() {
		snap, err := oauth.LoadVaultSnapshot()
		Expect(err).NotTo(HaveOccurred())
		_, err = snap.Token("nope")
		Expect(err).To(MatchError(transport.ErrNoToken))
	})
})
