package dustpan_test

import (
	"net/netip"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestNewServerTrustedProxies proves, through NewServer, how the trusted
// proxy settings set the client address of a request. A forwarding header
// from a peer outside the trusted list does not change the client address,
// in either header setting. A header from a trusted proxy does. A refused
// proxy list or an unknown header name makes NewServer fail, so the server
// never starts with them.
func TestNewServerTrustedProxies(t *testing.T) {
	t.Parallel()

	trusted := netip.MustParsePrefix("10.0.0.0/8")
	trustedV6 := netip.MustParsePrefix("fd00::/8")

	cases := []clientIPCase{
		{
			name:        "untrusted peer x-forwarded-for keeps peer",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote:      "198.51.100.7:1234",
			xff:         "203.0.113.9",
			fwd:         "for=203.0.113.9",
			want:        "198.51.100.7",
			wantMissing: false,
			absent:      "203.0.113.9",
		},
		{
			name: "untrusted peer forwarded keeps peer",
			extra: []dustpan.Option{
				dustpan.WithTrustedProxies(trusted),
				dustpan.WithTrustedProxyHeader("forwarded"),
			},
			remote:      "198.51.100.7:1234",
			xff:         "203.0.113.9",
			fwd:         "for=203.0.113.9",
			want:        "198.51.100.7",
			wantMissing: false,
			absent:      "203.0.113.9",
		},
		{
			name:        "untrusted ipv6 peer keeps peer",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trustedV6)},
			remote:      "[2001:db8::7]:1234",
			xff:         "203.0.113.9",
			fwd:         "",
			want:        "2001:db8::7",
			wantMissing: false,
			absent:      "203.0.113.9",
		},
		{
			name:        "no trusted proxies keeps peer",
			extra:       nil,
			remote:      "10.1.2.3:1234",
			xff:         "203.0.113.9",
			fwd:         "for=203.0.113.9",
			want:        "10.1.2.3",
			wantMissing: false,
			absent:      "203.0.113.9",
		},
		{
			name:        "trusted proxy x-forwarded-for sets client",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trusted)},
			remote:      "10.1.2.3:1234",
			xff:         "203.0.113.9",
			fwd:         "",
			want:        "203.0.113.9",
			wantMissing: false,
			absent:      "",
		},
		{
			name: "trusted proxy forwarded sets client",
			extra: []dustpan.Option{
				dustpan.WithTrustedProxies(trusted),
				dustpan.WithTrustedProxyHeader("Forwarded"),
			},
			remote:      "10.1.2.3:1234",
			xff:         "6.6.6.6",
			fwd:         "for=203.0.113.9",
			want:        "203.0.113.9",
			wantMissing: false,
			absent:      "6.6.6.6",
		},
		{
			name:        "trusted ipv6 proxy sets client",
			extra:       []dustpan.Option{dustpan.WithTrustedProxies(trustedV6)},
			remote:      "[fd00::1]:1234",
			xff:         "203.0.113.9",
			fwd:         "",
			want:        "203.0.113.9",
			wantMissing: false,
			absent:      "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkClientIPLog(t, tc)
		})
	}

	refused := []struct {
		name  string
		extra []dustpan.Option
		want  string
	}{
		{
			name:  "ipv4 list covers every address",
			extra: []dustpan.Option{dustpan.WithTrustedProxies(trusted, netip.MustParsePrefix("0.0.0.0/0"))},
			want:  "covers every address",
		},
		{
			name:  "ipv6 list covers every address",
			extra: []dustpan.Option{dustpan.WithTrustedProxies(netip.MustParsePrefix("::/0"))},
			want:  "covers every address",
		},
		{
			name:  "list has host bits",
			extra: []dustpan.Option{dustpan.WithTrustedProxies(netip.MustParsePrefix("10.0.0.1/8"))},
			want:  "has host bits set",
		},
		{
			name: "unknown header name",
			extra: []dustpan.Option{
				dustpan.WithTrustedProxies(trusted),
				dustpan.WithTrustedProxyHeader("x-real-ip"),
			},
			want: "is not x-forwarded-for or forwarded",
		},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), tc.extra...)

			_, err := dustpan.NewServer(opts...)
			if err == nil {
				t.Fatalf("NewServer with %s succeeded, want an error", tc.name)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewServer with %s: err = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}
