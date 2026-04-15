package mcp

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mark3labs/mcp-go/client/transport"
)

var _ = Describe("classifyAuthError", func() {
	DescribeTable("returns AuthRequiredError for auth-shaped errors",
		func(err error) {
			result := classifyAuthError("test", "https://example.com", err)
			Expect(result).NotTo(BeNil())
			var authErr *AuthRequiredError
			Expect(errors.As(result, &authErr)).To(BeTrue())
			Expect(authErr.Name).To(Equal("test"))
		},
		Entry("ErrUnauthorized", transport.ErrUnauthorized),
		Entry("ErrOAuthAuthorizationRequired", transport.ErrOAuthAuthorizationRequired),
		Entry("wrapped ErrUnauthorized", fmt.Errorf("something: %w", transport.ErrUnauthorized)),
	)

	DescribeTable("returns nil for non-auth errors",
		func(err error) {
			Expect(classifyAuthError("test", "https://example.com", err)).To(BeNil())
		},
		Entry("nil", nil),
		Entry("generic error", errors.New("connection refused")),
		Entry("timeout", errors.New("context deadline exceeded")),
	)
})

var _ = Describe("AuthRequiredError", func() {
	It("prints a user-actionable message", func() {
		err := &AuthRequiredError{Name: "linear", URL: "https://mcp.linear.app/sse"}
		Expect(err.Error()).To(ContainSubstring("squadron mcp login linear"))
		Expect(err.Error()).To(ContainSubstring("authorization required"))
	})

	It("unwraps to the cause", func() {
		cause := errors.New("underlying")
		err := &AuthRequiredError{Name: "x", Cause: cause}
		Expect(errors.Unwrap(err)).To(Equal(cause))
	})
})

var _ = Describe("isSSEURL", func() {
	DescribeTable("detects SSE URLs",
		func(url string, expected bool) {
			Expect(isSSEURL(url)).To(Equal(expected))
		},
		Entry("bare /sse", "https://example.com/sse", true),
		Entry("trailing slash", "https://example.com/sse/", true),
		Entry("nested path", "https://example.com/api/v1/sse", true),
		Entry("with query string", "https://example.com/sse?foo=bar", true),
		Entry("not SSE", "https://example.com/mcp", false),
		Entry("sse in host", "https://sse.example.com/api", false),
		Entry("sse substring", "https://example.com/message", false),
		Entry("empty", "", false),
	)
})
