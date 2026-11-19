package runner_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/runner"
)

// TestUpstreamEndpointChanged is the hostile table of the endpoint rule. The
// rule compares the parsed endpoint (scheme, host, port), never the text. A
// value that does not parse counts as a change. This test fails when the rule
// compares strings, or when a case below keeps a credential for another
// endpoint.
func TestUpstreamEndpointChanged(t *testing.T) {
	t.Parallel()

	const base = "https://proxy.example:3128"

	cases := []struct {
		name     string
		old, new string
		want     bool
	}{
		// The same endpoint keeps the credential.
		{"same text", base, base, false},
		{"path and query only", base, base + "/redirect?to=https://evil.example/x", false},
		{"host in upper case", base, "HTTPS://PROXY.EXAMPLE:3128", false},
		{"one trailing dot", base, "https://proxy.example.:3128", false},
		{"default port written", "https://proxy.example", "https://proxy.example:443", false},
		{"default http port written", "http://proxy.example", "http://proxy.example:80/", false},
		{"IPv6 other spelling", "http://[2001:db8::1]:3128", "http://[2001:DB8:0:0:0:0:0:1]:3128", false},
		{"IPv4 in IPv6 form", "http://10.0.0.1:3128", "http://[::ffff:10.0.0.1]:3128", false},
		{"nothing saved", "", "", false},

		// Another endpoint clears it.
		{"other host", base, "https://other.example:3128", true},
		{"subdomain", base, "https://a.proxy.example:3128", true},
		{"parent domain", base, "https://example:3128", true},
		{"suffix host", base, "https://proxy.example.net:3128", true},
		{"other scheme, same host", "https://proxy.example", "http://proxy.example:443", true},
		{"https to http", base, "http://proxy.example:3128", true},
		{"http to https", "http://proxy.example:3128", base, true},
		{"other port", base, "https://proxy.example:3129", true},
		{"default port against another", "https://proxy.example", "https://proxy.example:8443", true},
		{"two trailing dots", base, "https://proxy.example..:3128", true},
		{"user information trick", base, fakecred.URL("https", "proxy.example", "3128", "evil.example", ""), true},
		{
			"user information with the old host",
			base,
			fakecred.URL("https", "proxy.example", "3128", "evil.example:3128", ""),
			true,
		},
		{"backslash trick", base, `https://evil.example\@proxy.example:3128`, true},
		{"fragment trick", base, "https://evil.example:3128#@proxy.example", true},
		{"old host in the path", base, "https://evil.example/https://proxy.example:3128", true},
		{"old host in the query", base, "https://evil.example:3128/?h=proxy.example", true},
		{"Unicode look-alike", base, "https://prоxy.example:3128", true},
		{"punycode form", "https://münchen.example:3128", "https://xn--mnchen-3ya.example:3128", true},
		{"other address", "http://10.0.0.1:3128", "http://10.0.0.2:3128", true},
		{"address against name", "http://10.0.0.1:3128", "http://proxy.example:3128", true},
		{"decimal address", "http://10.0.0.1:3128", "http://167772161:3128", true},
		{"IPv6 zone", "http://[fe80::1]:3128", "http://[fe80::1%25eth0]:3128", true},
		{"endpoint removed", base, "", true},
		{"endpoint added", "", base, true},

		// A value that does not parse fails closed.
		{"old does not parse", "https://%zz", base, true},
		{"new does not parse", base, "https://%zz", true},
		{"both do not parse, same text", "https://%zz", "https://%zz", true},
		{"port out of range", base, "https://proxy.example:99999", true},
		{"port zero", "https://proxy.example:0", "https://proxy.example:0", true},
		{"port not a number", base, "https://proxy.example:abc", true},
		{"no host", "https:///x", "https:///x", true},
		{"relative", "proxy.example:3128", "proxy.example:3128", true},
		{"control character", base, "https://proxy.example\n:3128", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := runner.UpstreamEndpointChanged(tc.old, tc.new); got != tc.want {
				t.Errorf("UpstreamEndpointChanged(%q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
			}
		})
	}
}
