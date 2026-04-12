package oauth_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/mcp/oauth"
)

var _ = Describe("LoopbackCallbackSource", func() {
	var (
		source      *oauth.LoopbackCallbackSource
		redirectURI string
	)

	BeforeEach(func() {
		source = oauth.NewLoopbackCallbackSource()
		var err error
		redirectURI, err = source.Prepare(context.Background())
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		source.Close()
	})

	It("binds a loopback port and returns a valid redirect URI", func() {
		Expect(redirectURI).To(MatchRegexp(`^http://127\.0\.0\.1:\d+/callback$`))
	})

	It("delivers code and state from a successful callback", func() {
		go func() {
			defer GinkgoRecover()
			time.Sleep(50 * time.Millisecond)
			resp, err := http.Get(redirectURI + "?code=abc&state=xyz")
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		params, err := source.Wait(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Code).To(Equal("abc"))
		Expect(params.State).To(Equal("xyz"))
	})

	It("surfaces OAuth errors from the authorization server", func() {
		go func() {
			defer GinkgoRecover()
			time.Sleep(50 * time.Millisecond)
			resp, err := http.Get(redirectURI + "?error=access_denied&error_description=user+said+no")
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := source.Wait(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("access_denied"))
	})

	It("errors when the callback has no code", func() {
		go func() {
			defer GinkgoRecover()
			time.Sleep(50 * time.Millisecond)
			resp, err := http.Get(redirectURI + "?state=xyz")
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := source.Wait(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("without code"))
	})

	It("returns context error when Wait is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := source.Wait(ctx)
		Expect(err).To(MatchError(context.Canceled))
	})

	It("Close is idempotent", func() {
		Expect(source.Close()).To(Succeed())
		Expect(source.Close()).To(Succeed())
	})

	It("returns an HTML success page on valid callback", func() {
		resp, err := http.Get(redirectURI + "?code=abc&state=xyz")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.Header.Get("Content-Type")).To(ContainSubstring("text/html"))

		var body strings.Builder
		_, _ = fmt.Fscanf(resp.Body, "%s", &body)
	})
})
