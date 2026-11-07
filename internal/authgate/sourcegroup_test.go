package authgate_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
)

// TestSourceGroupKey checks the key of the site group: an IPv6 address is its
// /48, an IPv4 address is itself, an IPv4-mapped IPv6 address is the IPv4
// address, and a text with the scope separator is the one unknown source.
func TestSourceGroupKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, source, want string
	}{
		{"IPv4", "198.51.100.7", "198.51.100.7"},
		{"IPv6", "2001:db8:0:1:ffff::2", "2001:db8::/48"},
		{"two ranges of one /48", "2001:db8:0:ffff::1", "2001:db8::/48"},
		{"next /48", "2001:db8:1::1", "2001:db8:1::/48"},
		{"IPv4-mapped IPv6", "::ffff:198.51.100.7", "198.51.100.7"},
		{"scope separator", "a|b", "|"},
		{"not an address", "not-an-address", "not-an-address"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := authgate.SourceGroupKey(tc.source); got != tc.want {
				t.Errorf("SourceGroupKey(%q) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}
