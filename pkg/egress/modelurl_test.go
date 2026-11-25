package egress_test

import (
	"errors"
	"net/netip"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/egress"
)

// everything is an allowlist that has a rule for each address on the ports 80,
// 443 and 8443, with plain http, and that names each IP literal as a host. A
// URL that this allowlist lets through has no guard in front of the allowlist.
func everything() egress.Allowlist {
	ranges := []string{"0.0.0.0/0", "::/0"}
	ports := []int{80, 443, 8443}
	cidrs := make([]egress.CIDRRule, 0, len(ranges)*len(ports))

	for _, text := range ranges {
		for _, port := range ports {
			cidrs = append(cidrs, egress.CIDRRule{CIDR: netip.MustParsePrefix(text), Port: port, HTTP: true})
		}
	}

	return egress.Allowlist{
		Hosts: []string{
			"127.0.0.1", "169.254.169.254", "[::1]", "2130706433", "0x7f000001", "127.1", "localhost", "*.localhost",
		},
		CIDRs: cidrs,
	}
}

// TestCheckModelURL_DeniedRanges sends hostile URLs to the check of a model
// URL. The allowlist has a rule for every address, so each refusal comes from
// a guard of the check: the always-denied list, the shape of the URL, or the
// rule for a host that is a number.
func TestCheckModelURL_DeniedRanges(t *testing.T) {
	t.Parallel()

	urls := map[string]string{
		// Loopback.
		"loopback v4":                    "https://127.0.0.1/",
		"loopback v4 end":                "https://127.255.255.254/v1",
		"loopback v4 port":               "https://127.0.0.1:8443/",
		"loopback v4 http":               "http://127.0.0.1/",
		"loopback v6":                    "https://[::1]/",
		"loopback v6 http port":          "http://[::1]:8443/",
		"loopback mapped":                "https://[::ffff:127.0.0.1]/",
		"loopback mapped hex":            "https://[::ffff:7f00:1]/",
		"loopback mapped upper":          "https://[::FFFF:127.0.0.1]/",
		"loopback compatible v6":         "https://[::127.0.0.1]/",
		"loopback in nat64":              "https://[64:ff9b::7f00:1]/",
		"loopback in 6to4":               "https://[2002:7f00:1::]/",
		"loopback trailing dot":          "https://127.0.0.1./",
		"loopback with path and query":   "https://127.0.0.1/a/b?c=d",
		"loopback in nat64 local":        "https://[64:ff9b:1::7f00:1]/",
		"loopback in teredo":             "https://[2001::7f00:1]/",
		"loopback mapped compressed":     "https://[0:0:0:0:0:ffff:7f00:1]/",
		"loopback mapped expanded zeros": "https://[0000:0000:0000:0000:0000:ffff:7f00:0001]/",
		// Link-local and metadata.
		"metadata aws":              "https://169.254.169.254/latest/meta-data/",
		"metadata aws http":         "http://169.254.169.254/",
		"metadata aws mapped":       "https://[::ffff:169.254.169.254]/",
		"metadata aws mapped hex":   "https://[::ffff:a9fe:a9fe]/",
		"metadata ecs":              "https://169.254.170.2/",
		"link-local v4":             "https://169.254.1.1/",
		"link-local v4 start":       "https://169.254.0.1/",
		"metadata azure":            "https://168.63.129.16/",
		"metadata alibaba":          "https://100.100.100.200/",
		"metadata oracle":           "https://192.0.0.192/",
		"metadata aws v6":           "https://[fd00:ec2::254]/",
		"link-local v6":             "https://[fe80::1]/",
		"link-local v6 zone":        "https://[fe80::1%25eth0]/",
		"link-local v6 zone digits": "https://[fe80::a9fe:a9fe%250]/",
		"link-local v6 end":         "https://[febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff]/",
		// Unspecified, broadcast, multicast.
		"unspecified v4":   "https://0.0.0.0/",
		"this network":     "https://0.1.2.3/",
		"unspecified v6":   "https://[::]/",
		"broadcast":        "https://255.255.255.255/",
		"multicast v4":     "https://224.0.0.1/",
		"multicast v4 end": "https://239.255.255.255/",
		"multicast v6":     "https://[ff02::1]/",
		"multicast v6 all": "https://[ff0e::1]/",
		// A host that is a number in another form.
		"decimal ip":       "https://2130706433/",
		"decimal metadata": "https://2852039166/",
		"hex ip":           "https://0x7f000001/",
		"hex dotted":       "https://0x7f.0.0.1/",
		"short dotted":     "https://127.1/",
		"octal":            "https://017700000001/",
		"octal dotted":     "https://0177.0.0.1/",
		"hex metadata":     "https://0xa9fea9fe/",
		"zero":             "https://0/",
		// The shape of the URL.
		"user info":          fakecred.URL("https", "user", "secret", "api.example.com", "/"),
		"user only":          "https://user@api.example.com/",
		"empty user":         "https://@api.example.com/",
		"ftp":                "ftp://api.example.com/",
		"file":               "file:///etc/passwd",
		"gopher":             "gopher://api.example.com/",
		"javascript":         "javascript:alert(1)",
		"scheme upper ftp":   "FTP://api.example.com/",
		"no scheme":          "api.example.com/v1",
		"empty":              "",
		"no host":            "https:///path",
		"scheme only":        "https:",
		"opaque":             "https:api.example.com",
		"fragment":           "https://api.example.com/#frag",
		"empty fragment":     "https://api.example.com/#",
		"does not parse":     "https://api.example.com/%zz",
		"control character":  "https://api.example.com/\x00",
		"space in host":      "https://api example.com/",
		"localhost":          "https://localhost/",
		"localhost upper":    "https://LOCALHOST/",
		"localhost dot":      "https://localhost./",
		"subdomain of local": "https://api.localhost/",
	}

	for name, raw := range urls {
		for _, publicOnly := range []egress.PublicOnly{false, true} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := egress.CheckModelURL(raw, everything(), publicOnly)
				if !errors.Is(err, egress.ErrRefused) {
					t.Fatalf("CheckModelURL(%q, publicOnly=%v) = %v, want ErrRefused", raw, publicOnly, err)
				}
			})
		}
	}
}

// TestCheckModelURL_DeniedRangesAlone sends the always-denied addresses with an
// allowlist that has no rule at all. The check must refuse them too.
func TestCheckModelURL_DeniedRangesAlone(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://127.0.0.1/", "https://[::1]/", "https://169.254.169.254/", "https://[fe80::1%25eth0]/",
		"https://2130706433/", "https://0x7f000001/",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			err := egress.CheckModelURL(raw, egress.Allowlist{Hosts: nil, CIDRs: nil}, false)
			if !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckModelURL(%q) = %v, want ErrRefused", raw, err)
			}
		})
	}
}

// TestCheckModelURL_Allowed shows that the guards do not refuse a good URL.
// Without it, a check that refuses everything would pass the tests above.
func TestCheckModelURL_Allowed(t *testing.T) {
	t.Parallel()

	allow := egress.Allowlist{
		Hosts: []string{"api.example.com", "*.models.example.org"},
		CIDRs: []egress.CIDRRule{
			{CIDR: netip.MustParsePrefix("10.20.0.0/16"), Port: 8443, HTTP: false},
			{CIDR: netip.MustParsePrefix("10.30.0.0/16"), Port: 80, HTTP: true},
			{CIDR: netip.MustParsePrefix("fd12:3456::/32"), Port: 443, HTTP: false},
			{CIDR: netip.MustParsePrefix("93.184.216.0/24"), Port: 443, HTTP: false},
		},
	}

	tests := []struct {
		name       string
		raw        string
		publicOnly egress.PublicOnly
		wantErr    bool
	}{
		{"host", "https://api.example.com/v1", false, false},
		{"host upper and dot", "https://API.Example.com./v1", false, false},
		{"wildcard host", "https://eu.models.example.org/", false, false},
		{"wildcard base", "https://models.example.org/", false, false},
		{"host with public only", "https://api.example.com/v1", true, false},
		{"other host", "https://other.example.com/", false, true},
		{"suffix trick", "https://evilmodels.example.org/", false, true},
		{"host over http", "http://api.example.com/", false, true},
		{"private cidr", "https://10.20.1.1:8443/", false, false},
		{"private cidr wrong port", "https://10.20.1.1/", false, true},
		{"private cidr outside", "https://10.21.1.1:8443/", false, true},
		{"private cidr public only", "https://10.20.1.1:8443/", true, true},
		{"private cidr http denied", "http://10.20.1.1:8443/", false, true},
		{"private cidr http allowed", "http://10.30.1.1/", false, false},
		{"private v6 cidr", "https://[fd12:3456::1]/", false, false},
		{"private v6 cidr public only", "https://[fd12:3456::1]/", true, true},
		{"public cidr", "https://93.184.216.34/", false, false},
		{"public cidr public only", "https://93.184.216.34/", true, false},
		{"public address no cidr", "https://93.184.217.34/", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := egress.CheckModelURL(tt.raw, allow, tt.publicOnly)
			if tt.wantErr != errors.Is(err, egress.ErrRefused) || (err != nil && !tt.wantErr) {
				t.Fatalf(
					"CheckModelURL(%q, publicOnly=%v) = %v, want error: %v",
					tt.raw, tt.publicOnly, err, tt.wantErr,
				)
			}
		})
	}
}

// TestCheckModelAddrs_DeniedRanges sends the addresses that a host name
// resolved to. A name that resolves to a denied address, in any position of the
// answer, is refused whatever the allowlist says.
func TestCheckModelAddrs_DeniedRanges(t *testing.T) {
	t.Parallel()

	public := netip.MustParseAddr("93.184.216.34")

	denied := []string{
		"127.0.0.1", "127.1.2.3", "::1", "::ffff:127.0.0.1", "169.254.169.254", "::ffff:169.254.169.254",
		"169.254.170.2", "fe80::1", "fe80::1%eth0", "fd00:ec2::254", "100.100.100.200", "168.63.129.16",
		"192.0.0.192", "0.0.0.0", "::", "255.255.255.255", "224.0.0.1", "ff02::1", "64:ff9b::7f00:1", "2002:7f00:1::",
		"::127.0.0.1",
	}

	for _, text := range denied {
		runDeniedAddr(t, text, netip.MustParseAddr(text), public)
	}

	t.Run("no address", func(t *testing.T) {
		t.Parallel()

		err := egress.CheckModelAddrs("https://api.example.com/", nil, everything(), false)
		if !errors.Is(err, egress.ErrRefused) {
			t.Fatalf("CheckModelAddrs with no address = %v, want ErrRefused", err)
		}
	})

	t.Run("invalid address", func(t *testing.T) {
		t.Parallel()

		err := egress.CheckModelAddrs("https://api.example.com/", []netip.Addr{{}}, everything(), false)
		if !errors.Is(err, egress.ErrRefused) {
			t.Fatalf("CheckModelAddrs with a zero address = %v, want ErrRefused", err)
		}
	})

	t.Run("bad url", func(t *testing.T) {
		t.Parallel()

		err := egress.CheckModelAddrs("ftp://api.example.com/", []netip.Addr{public}, everything(), false)
		if !errors.Is(err, egress.ErrRefused) {
			t.Fatalf("CheckModelAddrs with a bad URL = %v, want ErrRefused", err)
		}
	})
}

// runDeniedAddr checks the denied address bad alone, after public and before
// public, with and without the flag --egress-public-only.
func runDeniedAddr(t *testing.T, text string, bad, public netip.Addr) {
	t.Helper()

	for name, addrs := range map[string][]netip.Addr{
		"alone": {bad}, "after public": {public, bad}, "before public": {bad, public},
	} {
		for _, publicOnly := range []egress.PublicOnly{false, true} {
			t.Run(text+" "+name, func(t *testing.T) {
				t.Parallel()

				err := egress.CheckModelAddrs("https://api.example.com/", addrs, everything(), publicOnly)
				if !errors.Is(err, egress.ErrRefused) {
					t.Fatalf("CheckModelAddrs(%v, publicOnly=%v) = %v, want ErrRefused", addrs, publicOnly, err)
				}
			})
		}
	}
}

// TestCheckModelAddrs_Allowed shows the addresses that pass.
func TestCheckModelAddrs_Allowed(t *testing.T) {
	t.Parallel()

	allow := egress.Allowlist{
		Hosts: []string{"api.example.com"},
		CIDRs: []egress.CIDRRule{{CIDR: netip.MustParsePrefix("10.20.0.0/16"), Port: 443, HTTP: false}},
	}
	public := netip.MustParseAddr("93.184.216.34")
	private := netip.MustParseAddr("10.20.1.1")
	outside := netip.MustParseAddr("10.99.1.1")

	tests := []struct {
		name       string
		addrs      []netip.Addr
		publicOnly egress.PublicOnly
		wantErr    bool
	}{
		{"public", []netip.Addr{public}, false, false},
		{"public with the flag", []netip.Addr{public}, true, false},
		{"private in cidr", []netip.Addr{private}, false, false},
		{"mapped private in cidr", []netip.Addr{netip.MustParseAddr("::ffff:10.20.1.1")}, false, false},
		{"private in cidr public only", []netip.Addr{private}, true, true},
		{"private outside cidr", []netip.Addr{outside}, false, true},
		{"public and private outside", []netip.Addr{public, outside}, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := egress.CheckModelAddrs("https://api.example.com/", tt.addrs, allow, tt.publicOnly)
			if tt.wantErr != errors.Is(err, egress.ErrRefused) || (err != nil && !tt.wantErr) {
				t.Fatalf(
					"CheckModelAddrs(%v, publicOnly=%v) = %v, want error: %v",
					tt.addrs, tt.publicOnly, err, tt.wantErr,
				)
			}
		})
	}
}

// TestAllowlist_CannotAllowDeniedRange puts a CIDR rule that covers a denied
// range in the allowlist. The rule must not open the range.
func TestAllowlist_CannotAllowDeniedRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cidr string
		raw  string
	}{
		{"loopback v4 exact", "127.0.0.1/32", "https://127.0.0.1/"},
		{"loopback v4 range", "127.0.0.0/8", "https://127.0.0.1/"},
		{"loopback v4 all", "0.0.0.0/0", "https://127.0.0.1/"},
		{"loopback v6 exact", "::1/128", "https://[::1]/"},
		{"loopback v6 all", "::/0", "https://[::1]/"},
		{"loopback mapped range", "::ffff:0:0/96", "https://[::ffff:127.0.0.1]/"},
		{"loopback mapped exact", "::ffff:127.0.0.1/128", "https://127.0.0.1/"},
		{"link-local range", "169.254.0.0/16", "https://169.254.1.1/"},
		{"metadata exact", "169.254.169.254/32", "https://169.254.169.254/"},
		{"metadata range", "169.254.169.0/24", "https://169.254.169.254/"},
		{"metadata all", "0.0.0.0/0", "https://169.254.169.254/"},
		{"metadata mapped", "::ffff:169.254.169.254/128", "https://169.254.169.254/"},
		{"metadata ecs", "169.254.170.2/32", "https://169.254.170.2/"},
		{"metadata alibaba", "100.100.100.200/32", "https://100.100.100.200/"},
		{"metadata azure", "168.63.129.16/32", "https://168.63.129.16/"},
		{"metadata oracle", "192.0.0.192/32", "https://192.0.0.192/"},
		{"metadata v6 exact", "fd00:ec2::254/128", "https://[fd00:ec2::254]/"},
		{"metadata v6 unique local", "fc00::/7", "https://[fd00:ec2::254]/"},
		{"link-local v6", "fe80::/10", "https://[fe80::1]/"},
		{"link-local v6 zone", "fe80::/10", "https://[fe80::1%25eth0]/"},
		{"unspecified v4", "0.0.0.0/8", "https://0.0.0.0/"},
		{"unspecified v6", "::/128", "https://[::]/"},
		{"broadcast", "255.255.255.255/32", "https://255.255.255.255/"},
		{"multicast v4", "224.0.0.0/4", "https://224.0.0.1/"},
		{"multicast v6", "ff00::/8", "https://[ff02::1]/"},
		{"nat64 loopback", "64:ff9b::/96", "https://[64:ff9b::7f00:1]/"},
		{"6to4 loopback", "2002::/16", "https://[2002:7f00:1::]/"},
	}

	for _, tt := range tests {
		for _, publicOnly := range []egress.PublicOnly{false, true} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				cidr := netip.MustParsePrefix(tt.cidr)
				allow := egress.Allowlist{
					Hosts: nil,
					CIDRs: []egress.CIDRRule{
						{CIDR: cidr, Port: 443, HTTP: true},
						{CIDR: cidr, Port: 80, HTTP: true},
					},
				}

				err := egress.CheckModelURL(tt.raw, allow, publicOnly)
				if !errors.Is(err, egress.ErrRefused) {
					t.Fatalf("CheckModelURL(%q) with the rule %s = %v, want ErrRefused", tt.raw, tt.cidr, err)
				}
			})
		}
	}
}

// TestAllowlist_PublicOnlyDeniesPrivateRule shows that the flag
// --egress-public-only closes a private range that the allowlist opens, and
// leaves a public range open.
func TestAllowlist_PublicOnlyDeniesPrivateRule(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "198.18.0.1", "fd12::1", "fc00::1",
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			addr := netip.MustParseAddr(text)
			allow := egress.Allowlist{
				Hosts: nil,
				CIDRs: []egress.CIDRRule{{CIDR: netip.PrefixFrom(addr, addr.BitLen()), Port: 443, HTTP: false}},
			}

			host := text
			if addr.Is6() {
				host = "[" + text + "]"
			}

			raw := "https://" + host + "/"

			if err := egress.CheckModelURL(raw, allow, false); err != nil {
				t.Fatalf("without the flag, CheckModelURL(%q) = %v, want nil", raw, err)
			}

			if err := egress.CheckModelURL(raw, allow, true); !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("with the flag, CheckModelURL(%q) = %v, want ErrRefused", raw, err)
			}
		})
	}
}
