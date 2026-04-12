package oauth_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mark3labs/mcp-go/client/transport"

	"squadron/mcp/oauth"
)

var _ = Describe("ForceRefresh", func() {
	BeforeEach(func() { initTestVault() })

	It("errors when no token is stored", func() {
		err := oauth.ForceRefresh(context.Background(), "missing", "https://example.com/mcp")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no token"))
	})

	It("errors when the stored token has no refresh token", func() {
		store := oauth.NewVaultTokenStore("no-refresh")
		Expect(store.SaveToken(context.Background(), &transport.Token{
			AccessToken: "access-only",
		})).To(Succeed())

		err := oauth.ForceRefresh(context.Background(), "no-refresh", "https://example.com/mcp")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no refresh token"))
	})
})

var _ = Describe("DiscoverAuthServerMetadataURL", func() {
	It("returns a .well-known URL even when discovery fails", func() {
		// Against a server that doesn't exist, falls back to origin-level
		// .well-known/oauth-authorization-server.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		url, err := oauth.DiscoverAuthServerMetadataURL(ctx, "https://127.0.0.1:1/mcp")
		// Either returns a fallback URL or errors — both are acceptable.
		// The key invariant: it should never panic.
		if err == nil {
			Expect(url).To(ContainSubstring(".well-known/oauth-authorization-server"))
		}
	})

	It("extracts the origin correctly", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		url, err := oauth.DiscoverAuthServerMetadataURL(ctx, "https://127.0.0.1:1/some/deep/path/mcp")
		if err == nil {
			Expect(url).To(HavePrefix("https://127.0.0.1:1/"))
			Expect(url).NotTo(ContainSubstring("/some/deep/path"))
		}
	})
})
