package dustpan

import (
	"net/netip"
	"slices"
	"testing"
)

// TestLDAPConfigTakesTheAllowedRanges proves that the egress rules of the
// LDAP method hold the allowed ranges of the provider and the loopback switch
// of the server, and nothing more: a provider with no range opens no private
// address.
func TestLDAPConfigTakesTheAllowedRanges(t *testing.T) {
	t.Parallel()

	ranges := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}

	for _, tc := range []struct {
		name     string
		ranges   []netip.Prefix
		loopback bool
	}{
		{name: "no range", ranges: nil, loopback: false},
		{name: "a private range", ranges: ranges, loopback: false},
		{name: "loopback for tests", ranges: nil, loopback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				cfg settings
				p   LDAPProvider
			)

			cfg.ldapLoopback = tc.loopback
			p.AllowedRanges = tc.ranges

			got := ldapConfig(&cfg, &p, nil).Egress
			if !slices.Equal(got.Allow, tc.ranges) || got.LoopbackForTests != tc.loopback {
				t.Fatalf("egress rules = %+v, want the ranges %v and loopback %t", got, tc.ranges, tc.loopback)
			}
		})
	}
}
