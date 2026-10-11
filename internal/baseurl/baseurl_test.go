package baseurl_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/baseurl"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr error
	}{
		{name: "empty means no public URL", raw: "", want: "", wantErr: nil},
		{name: "https host", raw: "https://dustpan.example", want: "https://dustpan.example", wantErr: nil},
		{name: "http host with port", raw: "http://localhost:8080", want: "http://localhost:8080", wantErr: nil},
		{name: "path prefix is kept", raw: "https://host/prefix", want: "https://host/prefix", wantErr: nil},
		{name: "trailing slash is dropped", raw: "https://host/", want: "https://host", wantErr: nil},
		{name: "not a URL", raw: "not-a-url", want: "", wantErr: baseurl.ErrInvalid},
		{name: "relative path", raw: "/prefix", want: "", wantErr: baseurl.ErrInvalid},
		{name: "wrong scheme", raw: "ftp://host", want: "", wantErr: baseurl.ErrInvalid},
		{name: "no host", raw: "https://", want: "", wantErr: baseurl.ErrInvalid},
		{name: "query is refused", raw: "https://host?x=1", want: "", wantErr: baseurl.ErrInvalid},
		{name: "unparsable", raw: "https://ho%st", want: "", wantErr: baseurl.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := baseurl.Parse(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Parse(%q) err = %v, want %v", tc.raw, err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Parse(%q) err = %v", tc.raw, err)
			}

			if got.String() != tc.want {
				t.Fatalf("Parse(%q) = %q, want %q", tc.raw, got.String(), tc.want)
			}
		})
	}
}

func TestBaseURL(t *testing.T) {
	t.Parallel()

	public, err := baseurl.Parse("https://dustpan.example")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	plain := httptest.NewRequestWithContext(
		t.Context(),
		"GET",
		"http://internal.example:8080/api/user",
		nil,
	)

	forwarded := httptest.NewRequestWithContext(
		t.Context(),
		"GET",
		"http://internal.example:8080/api/user",
		nil,
	)
	forwarded.Header.Set("X-Forwarded-Proto", "https")
	forwarded.Header.Set("X-Forwarded-Host", "evil.example")
	forwarded.Header.Set("X-Forwarded-Port", "443")

	tlsReq := httptest.NewRequestWithContext(
		t.Context(),
		"GET",
		"https://internal.example:8443/api/user",
		nil,
	)
	//nolint:exhaustruct_v5 // The test needs only a non-nil TLS state to select the https scheme.
	tlsReq.TLS = &tls.ConnectionState{}

	cases := []struct {
		name     string
		resolver baseurl.Resolver
		request  *http.Request
		want     string
	}{
		{name: "public URL wins", resolver: public, request: plain, want: "https://dustpan.example"},
		{
			name:     "public URL wins over proxy headers",
			resolver: public,
			request:  forwarded,
			want:     "https://dustpan.example",
		},
		{
			name:     "plain request uses http",
			resolver: baseurl.Resolver{},
			request:  plain,
			want:     "http://internal.example:8080",
		},
		{
			name:     "proxy headers are ignored",
			resolver: baseurl.Resolver{},
			request:  forwarded,
			want:     "http://internal.example:8080",
		},
		{
			name:     "TLS request uses https",
			resolver: baseurl.Resolver{},
			request:  tlsReq,
			want:     "https://internal.example:8443",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.resolver.BaseURL(tc.request); got != tc.want {
				t.Fatalf("BaseURL = %q, want %q", got, tc.want)
			}
		})
	}
}
