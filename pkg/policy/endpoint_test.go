package policy_test

import (
	"errors"
	"net/netip"
	"testing"

	"opendustpan.org/opendustpan/pkg/policy"
)

func TestAllowlistCheckURL(t *testing.T) {
	t.Parallel()

	allow := policy.Allowlist{
		Hosts:   []string{"opa.example.com", "hooks.example.com", "10.0.0.5", "127.0.0.1"},
		Private: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
	}

	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{"an allowed host", "https://opa.example.com/v1/data/catalog", true},
		{"an allowed host with a port", "https://hooks.example.com:8443/policy", true},
		{"the host in another case", "https://OPA.example.com/v1/data/catalog", true},
		{"an allowed private IP literal", "https://10.0.0.5/v1/data", true},
		{"plain http", "http://opa.example.com/v1/data/catalog", false},
		{"a host that is not on the list", "https://evil.example.net/", false},
		{"user info", "https://user:pass@opa.example.com/", false},
		{"a fragment", "https://opa.example.com/#x", false},
		{"loopback on the list but not in a private prefix", "https://127.0.0.1/", false},
		{"metadata address", "https://169.254.169.254/latest", false},
		{"no host", "https:///path", false},
		{"not a URL", "https://%zz", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := allow.CheckURL(tc.url)
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, policy.ErrEndpoint)) {
				t.Fatalf("CheckURL(%q) = %v, want ok %v", tc.url, err, tc.ok)
			}
		})
	}
}

func TestAllowlistCheckAddr(t *testing.T) {
	t.Parallel()

	allow := policy.Allowlist{Hosts: nil, Private: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}}

	cases := []struct {
		addr string
		ok   bool
	}{
		{"93.184.216.34", true},
		{"2606:2800:220:1:248:1893:25c8:1946", true},
		{"10.1.2.3", true},
		{"::ffff:10.1.2.3", true},
		{"10.2.0.1", false},
		{"192.168.1.1", false},
		{"172.16.0.1", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"fd00::1", false},
		{"0.0.0.0", false},
		{"::", false},
		{"100.64.0.1", false},
		{"224.0.0.1", false},
		{"::ffff:127.0.0.1", false},
		{"64:ff9b::a00:1", false},
		{"198.18.0.1", false},
	}

	for _, tc := range cases {
		err := allow.CheckAddr(netip.MustParseAddr(tc.addr))
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, policy.ErrEndpoint)) {
			t.Fatalf("CheckAddr(%s) = %v, want ok %v", tc.addr, err, tc.ok)
		}
	}

	if err := allow.CheckAddr(netip.Addr{}); !errors.Is(err, policy.ErrEndpoint) {
		t.Fatalf("CheckAddr(zero) = %v, want ErrEndpoint", err)
	}
}
