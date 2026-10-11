package pulumi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/baseurl"
)

// TestExternalBaseURL proves the handler reports the public URL from its
// Config, and otherwise the scheme and host of the request without reading
// proxy headers.
func TestExternalBaseURL(t *testing.T) {
	t.Parallel()

	public, err := baseurl.Parse("https://dustpan.example")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	withPublic := newTestHandlerWithBaseURL(t, public)
	withoutPublic := newTestHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"http://internal.example:8080/api/user",
		http.NoBody,
	)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "proxy.example")

	if got := withPublic.ExternalBaseURL(req); got != "https://dustpan.example" {
		t.Fatalf("ExternalBaseURL with a public URL = %q, want %q", got, "https://dustpan.example")
	}

	if got := withoutPublic.ExternalBaseURL(req); got != "http://internal.example:8080" {
		t.Fatalf("ExternalBaseURL without a public URL = %q, want %q", got, "http://internal.example:8080")
	}
}
