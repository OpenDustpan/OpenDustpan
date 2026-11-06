package egress_test

import (
	"errors"
	"net/netip"
	"net/url"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/egress"
)

func mustRanges(t *testing.T, list ...string) egress.Rules {
	t.Helper()

	ranges, err := egress.ParseRanges(list)
	if err != nil {
		t.Fatalf("ParseRanges(%v) = %v", list, err)
	}

	return egress.Rules{Allow: ranges, LoopbackForTests: false}
}

func TestCheckAddrRefusesCloudMetadataAddresses(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{
		"169.254.169.254", "169.254.170.2", "168.63.129.16", "100.100.100.200", "192.0.0.192", "fd00:ec2::254",
		"::ffff:168.63.129.16",
	} {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()

			if err := egress.CheckAddr(netip.MustParseAddr(addr)); !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckAddr(%s) = %v, want ErrRefused", addr, err)
			}
		})
	}
}

func TestParseRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		ok   bool
	}{
		{"none", nil, true},
		{"two ranges", []string{"10.0.0.0/8", " 127.0.0.0/8 "}, true},
		{"an unmasked range", []string{"10.1.2.3/8"}, true},
		{"an address", []string{"10.0.0.1"}, false},
		{"an empty entry", []string{""}, false},
		{"text", []string{"intranet"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := egress.ParseRanges(tt.in)
			if tt.ok != (err == nil) {
				t.Fatalf("ParseRanges(%v) = %v, want ok %v", tt.in, err, tt.ok)
			}
		})
	}
}

// TestRulesAddressLayer sends hostile addresses straight to the address
// check of the rules.
func TestRulesAddressLayer(t *testing.T) {
	t.Parallel()

	rules := mustRanges(t, "10.0.0.0/8", "127.0.0.0/8")

	tests := []struct {
		addr string
		ok   bool
	}{
		{"10.1.2.3", true},
		{"127.0.0.1", false},
		{"::ffff:10.1.2.3", true},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"::1", false},
		{"93.184.215.14", true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()

			err := rules.CheckAddr(netip.MustParseAddr(tt.addr))
			if tt.ok != (err == nil) {
				t.Fatalf("CheckAddr(%s) = %v, want ok %v", tt.addr, err, tt.ok)
			}
		})
	}

	if err := rules.CheckAddr(netip.Addr{}); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("CheckAddr of an invalid address = %v, want ErrRefused", err)
	}

	zero := egress.Rules{Allow: nil, LoopbackForTests: false}

	if err := zero.CheckAddr(netip.MustParseAddr("10.1.2.3")); err == nil {
		t.Fatal("the zero rules allowed a private address")
	}
}

// TestRulesURLLayer checks that an allowed range never lets a URL of a bad
// shape pass.
func TestRulesURLLayer(t *testing.T) {
	t.Parallel()

	rules := mustRanges(t, "10.0.0.0/8", "127.0.0.0/8")

	tests := []struct {
		url string
		ok  bool
	}{
		{"https://10.1.2.3/api", true},
		{"https://127.0.0.1:8443/api/v3", false},
		{"http://10.1.2.3/api", false},
		{fakecred.URL("https", "user", "pw", "10.1.2.3", ""), false},
		{"https://10.1.2.3/#frag", false},
		{"https://localhost/", false},
		{"https://2130706433/", false},
		{"https://0x7f000001/", false},
		{"https://192.168.1.1/", false},
		{"https://example.com/", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()

			err := rules.CheckURL(tt.url, egress.HTTPSOnly)
			if tt.ok != (err == nil) {
				t.Fatalf("CheckURL(%s) = %v, want ok %v", tt.url, err, tt.ok)
			}

			if err != nil && !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckURL(%s) = %v, want ErrRefused", tt.url, err)
			}
		})
	}
}

// TestRulesRedirectLayer checks the redirect check with an allowed range.
func TestRulesRedirectLayer(t *testing.T) {
	t.Parallel()

	rules := mustRanges(t, "10.0.0.0/8")
	from, err := url.Parse("https://10.1.2.3/a")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		to       string
		redirect egress.Redirects
		ok       bool
	}{
		{"https://10.1.2.3/b", egress.SameHost, true},
		{"https://10.1.2.4/b", egress.SameHost, false},
		{"https://192.168.0.1/b", egress.SameHost, false},
		{"http://10.1.2.3/b", egress.SameHost, false},
		{"https://10.1.2.3/b", egress.NoRedirect, false},
	}

	for _, tt := range tests {
		t.Run(tt.to, func(t *testing.T) {
			t.Parallel()

			to, perr := url.Parse(tt.to)
			if perr != nil {
				t.Fatal(perr)
			}

			err := rules.CheckRedirect(from, to, tt.redirect, egress.HTTPSOnly)
			if tt.ok != (err == nil) {
				t.Fatalf("CheckRedirect(%s) = %v, want ok %v", tt.to, err, tt.ok)
			}
		})
	}
}

// TestAllowedRangeOpensPrivateAddressesOnly sends each address that an
// allowed range must never open straight to the address check, with a range
// that holds it.
func TestAllowedRangeOpensPrivateAddressesOnly(t *testing.T) {
	t.Parallel()

	rules := mustRanges(t,
		"10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "0.0.0.0/8", "224.0.0.0/8", "100.64.0.0/10",
		"fc00::/7", "::1/128", "fe80::/10", "ff00::/8", "::/128", "::/96",
	)

	tests := []struct {
		name string
		addr string
		ok   bool
	}{
		{"RFC 1918", "10.1.2.3", true},
		{"unique local", "fd12:3456::1", true},
		{"loopback", "127.0.0.1", false},
		{"IPv6 loopback", "::1", false},
		{"mapped loopback", "::ffff:127.0.0.1", false},
		{"link-local", "169.254.1.1", false},
		{"IPv6 link-local", "fe80::1", false},
		{"cloud metadata", "169.254.169.254", false},
		{"IPv6 cloud metadata", "fd00:ec2::254", false},
		{"unspecified", "0.0.0.0", false},
		{"IPv6 unspecified", "::", false},
		{"multicast", "224.0.0.1", false},
		{"IPv6 multicast", "ff02::1", false},
		{"shared address space", "100.64.1.1", false},
		{"Alibaba metadata", "100.100.100.200", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := rules.CheckAddr(netip.MustParseAddr(tt.addr))
			if tt.ok != (err == nil) {
				t.Fatalf("CheckAddr(%s) = %v, want ok %v", tt.addr, err, tt.ok)
			}

			if err != nil && !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckAddr(%s) = %v, want ErrRefused", tt.addr, err)
			}
		})
	}
}

func TestParseAllowedRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want error
	}{
		{"none", nil, nil},
		{"a /8", []string{"10.0.0.0/8"}, nil},
		{"a /16 and a /32 range", []string{"192.168.0.0/16", "fd00::/32"}, nil},
		{"the default route", []string{"0.0.0.0/0"}, egress.ErrRangeTooWide},
		{"the IPv6 default route", []string{"::/0"}, egress.ErrRangeTooWide},
		{"a /7", []string{"10.0.0.0/7"}, egress.ErrRangeTooWide},
		{"an IPv6 /31", []string{"fc00::/31"}, egress.ErrRangeTooWide},
		{"an IPv6 /16", []string{"fc00::/16"}, egress.ErrRangeTooWide},
		{"a mapped default route", []string{"::ffff:0:0/96"}, egress.ErrRangeTooWide},
		{"loopback", []string{"127.0.0.0/8"}, egress.ErrRangeTooWide},
		{"link-local", []string{"169.254.0.0/16"}, egress.ErrRangeTooWide},
		{"IPv6 link-local", []string{"fe80::/32"}, egress.ErrRangeTooWide},
		{"multicast", []string{"224.0.0.0/8"}, egress.ErrRangeTooWide},
		{"unspecified", []string{"0.0.0.0/8"}, egress.ErrRangeTooWide},
		{"one good range and one wide", []string{"10.0.0.0/8", "0.0.0.0/0"}, egress.ErrRangeTooWide},
		{"text", []string{"intranet"}, egress.ErrRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := egress.ParseAllowedRanges(tt.in)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("ParseAllowedRanges(%v) = %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

// TestLoopbackFlagOpensLoopbackOnly checks that the flag of the tests opens no
// other class.
func TestLoopbackFlagOpensLoopbackOnly(t *testing.T) {
	t.Parallel()

	rules := egress.Rules{Allow: nil, LoopbackForTests: true}

	for addr, ok := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.0.0.1": false, "169.254.169.254": false, "0.0.0.0": false,
	} {
		if err := rules.CheckAddr(netip.MustParseAddr(addr)); ok != (err == nil) {
			t.Fatalf("CheckAddr(%s) = %v, want ok %v", addr, err, ok)
		}
	}
}
