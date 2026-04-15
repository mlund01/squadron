package oauth_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/mcp/oauth"
)

// mockCallbackSource records what the flow did and returns canned responses.
type mockCallbackSource struct {
	redirectURI string
	prepareErr  error
	presentErr  error
	waitParams  oauth.CallbackParams
	waitErr     error

	presentedURL string
	closed       bool
}

func (m *mockCallbackSource) Prepare(_ context.Context) (string, error) {
	return m.redirectURI, m.prepareErr
}
func (m *mockCallbackSource) Present(_ context.Context, authURL string) error {
	m.presentedURL = authURL
	return m.presentErr
}
func (m *mockCallbackSource) Wait(_ context.Context) (oauth.CallbackParams, error) {
	return m.waitParams, m.waitErr
}
func (m *mockCallbackSource) Close() error {
	m.closed = true
	return nil
}

var _ = Describe("RunLoginFlow", func() {
	It("rejects empty name", func() {
		err := oauth.RunLoginFlow(context.Background(), "", "https://example.com/mcp", &mockCallbackSource{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires name"))
	})

	It("rejects empty server URL", func() {
		err := oauth.RunLoginFlow(context.Background(), "test", "", &mockCallbackSource{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("requires name"))
	})

	It("wraps Prepare errors", func() {
		source := &mockCallbackSource{prepareErr: errors.New("bind failed")}
		err := oauth.RunLoginFlow(context.Background(), "test", "https://example.com/mcp", source)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("preparing callback"))
		Expect(err.Error()).To(ContainSubstring("bind failed"))
	})

	It("closes the source on all exit paths", func() {
		// Prepare succeeds, but discovery will fail (no real server) — Close should still run.
		initTestVault()
		source := &mockCallbackSource{redirectURI: "http://127.0.0.1:9999/callback"}
		// Use a short timeout so the HTTP call to the fake server fails fast.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = oauth.RunLoginFlow(ctx, "test", "https://127.0.0.1:1/mcp", source)
		Expect(source.closed).To(BeTrue())
	})
})
