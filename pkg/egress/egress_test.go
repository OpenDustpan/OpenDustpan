package egress_test

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/egress"
)

func TestCheckAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		ok   bool
	}{
		{"93.184.215.14", true},
		{"2606:2800:21f:cb07:6820:80da:af6b:8b2c", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"0.0.0.0", false},
		{"::", false},
		{"100.64.0.1", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"fd00::1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:169.254.169.254", false},
		{"64:ff9b::a9fe:a9fe", false},
		{"2002:a9fe:a9fe::1", false},
		{"255.255.255.255", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()

			err := egress.CheckAddr(netip.MustParseAddr(tt.addr))
			if tt.ok != (err == nil) {
				t.Fatalf("CheckAddr(%s) = %v, want ok %v", tt.addr, err, tt.ok)
			}

			if err != nil && !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckAddr(%s) = %v, want ErrRefused", tt.addr, err)
			}
		})
	}
}

func TestCheckURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		url    string
		scheme egress.Scheme
		ok     bool
	}{
		{"https name", "https://hooks.example.com/x", egress.HTTPSOnly, true},
		{"http refused for https only", "http://hooks.example.com/x", egress.HTTPSOnly, false},
		{"http allowed", "http://cal.example.com/a.ics", egress.HTTPOrHTTPS, true},
		{"metadata address", "https://169.254.169.254/", egress.HTTPSOnly, false},
		{"metadata address over http", "http://169.254.169.254/", egress.HTTPOrHTTPS, false},
		{"loopback v6 with port", "https://[::1]:8443/", egress.HTTPSOnly, false},
		{"mapped loopback", "https://[::ffff:7f00:1]/", egress.HTTPSOnly, false},
		{"localhost", "https://localhost/", egress.HTTPSOnly, false},
		{"localhost subdomain", "https://a.localhost./", egress.HTTPSOnly, false},
		{"userinfo", "https://user:pw@hooks.example.com/", egress.HTTPSOnly, false},
		{"fragment", "https://hooks.example.com/#x", egress.HTTPSOnly, false},
		{"no host", "https:///x", egress.HTTPSOnly, false},
		{"other scheme", "file:///etc/passwd", egress.HTTPOrHTTPS, false},
		{"does not parse", "https://%zz", egress.HTTPSOnly, false},
		{"too long", "https://a.example.com/" + strings.Repeat("a", egress.MaxURLBytes), egress.HTTPSOnly, false},
		{"public address", "https://93.184.215.14/", egress.HTTPSOnly, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := egress.CheckURL(tt.url, tt.scheme)
			if tt.ok != (err == nil) {
				t.Fatalf("CheckURL(%q) = %v, want ok %v", tt.url, err, tt.ok)
			}

			if err != nil && !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckURL(%q) = %v, want ErrRefused", tt.url, err)
			}
		})
	}
}

func TestCheckRedirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		from, to string
		policy   egress.Redirects
		scheme   egress.Scheme
		ok       bool
	}{
		{
			"none for a webhook",
			"https://a.example.com/",
			"https://a.example.com/b",
			egress.NoRedirect,
			egress.HTTPSOnly,
			false,
		},
		{"same host", "https://a.example.com/", "https://a.example.com/b", egress.SameHost, egress.HTTPOrHTTPS, true},
		{"other host", "https://a.example.com/", "https://b.example.com/", egress.SameHost, egress.HTTPOrHTTPS, false},
		{"downgrade", "https://a.example.com/", "http://a.example.com/", egress.SameHost, egress.HTTPOrHTTPS, false},
		{"upgrade", "http://a.example.com/", "https://a.example.com/", egress.SameHost, egress.HTTPOrHTTPS, true},
		{"to a private address", "https://10.0.0.1/", "https://10.0.0.1/b", egress.SameHost, egress.HTTPOrHTTPS, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			from, err := url.Parse(tt.from)
			if err != nil {
				t.Fatal(err)
			}

			to, err := url.Parse(tt.to)
			if err != nil {
				t.Fatal(err)
			}

			err = egress.CheckRedirect(from, to, tt.policy, tt.scheme)
			if tt.ok != (err == nil) {
				t.Fatalf("CheckRedirect = %v, want ok %v", err, tt.ok)
			}
		})
	}
}
