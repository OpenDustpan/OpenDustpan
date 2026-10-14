package dustpan

import (
	"net/netip"
	"testing"
)

// TestGateSource proves the authentication gate counts failures by the
// resolved client address, with one shared bucket for unknown addresses.
func TestGateSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr netip.Addr
		want string
	}{
		{name: "ipv4", addr: netip.MustParseAddr("203.0.113.9"), want: "203.0.113.9"},
		{name: "ipv6", addr: netip.MustParseAddr("2001:db8::1"), want: "2001:db8::1"},
		{name: "unknown", addr: netip.Addr{}, want: "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := gateSource(tc.addr); got != tc.want {
				t.Fatalf("gateSource = %q, want %q", got, tc.want)
			}
		})
	}
}
