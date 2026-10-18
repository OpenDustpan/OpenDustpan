package httpexec_test

import (
	"errors"
	"testing"

	"opendustpan.org/providerkit/httpexec"
)

func origin(t *testing.T, raw string) httpexec.Origin {
	t.Helper()

	o, err := httpexec.OriginOf(raw)
	if err != nil {
		t.Fatalf("OriginOf(%q) error = %v", raw, err)
	}

	return o
}

func TestOriginCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url string
		ok  bool
	}{
		{url: "https://api.example.org/v1/items?page=2", ok: true},
		{url: "https://API.example.org:443/other", ok: true},
		{url: "https://api.example.org:8443/v1", ok: false},
		{url: "http://api.example.org/v1", ok: false},
		{url: "https://evil.example.org/v1", ok: false},
		{url: "https://api.example.org.evil.example/v1", ok: false},
		{url: "//evil.example.org/v1", ok: false},
		{url: "/v1/items", ok: false},
		{url: "file:///etc/passwd", ok: false},
	}

	o := origin(t, "https://api.example.org/v1")

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()

			err := o.Check(tt.url)
			if tt.ok && err != nil {
				t.Fatalf("Check(%q) = %v, want nil", tt.url, err)
			}

			if !tt.ok && !errors.Is(err, httpexec.ErrOtherOrigin) {
				t.Fatalf("Check(%q) = %v, want ErrOtherOrigin", tt.url, err)
			}
		})
	}
}

func TestOriginResolve(t *testing.T) {
	t.Parallel()

	const request = "https://api.example.org/v1/items?page=1"

	tests := []struct {
		link string
		want string
		ok   bool
	}{
		{link: "https://api.example.org/v1/items?page=2", want: "https://api.example.org/v1/items?page=2", ok: true},
		{link: "/v1/items?page=2", want: "https://api.example.org/v1/items?page=2", ok: true},
		{link: "items?page=2", want: "https://api.example.org/v1/items?page=2", ok: true},
		{link: "?page=2", want: "https://api.example.org/v1/items?page=2", ok: true},
		{link: "https://collector.example.net/steal", want: "", ok: false},
		{link: "//collector.example.net/steal", want: "", ok: false},
		{link: "http://api.example.org/v1/items?page=2", want: "", ok: false},
		{link: "https://api.example.org:444/v1/items", want: "", ok: false},
		{link: "%zz", want: "", ok: false},
	}

	o := origin(t, "https://api.example.org")

	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			t.Parallel()

			got, err := o.Resolve(request, tt.link)
			if tt.ok && (err != nil || got != tt.want) {
				t.Fatalf("Resolve(%q) = %q, %v, want %q", tt.link, got, err, tt.want)
			}

			if !tt.ok && !errors.Is(err, httpexec.ErrOtherOrigin) {
				t.Fatalf("Resolve(%q) error = %v, want ErrOtherOrigin", tt.link, err)
			}
		})
	}
}

func TestOriginOfRefuses(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "/v1", "api.example.org", "ftp://api.example.org", "https://"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			if _, err := httpexec.OriginOf(raw); err == nil {
				t.Fatalf("OriginOf(%q) error = nil, want an error", raw)
			}
		})
	}
}
