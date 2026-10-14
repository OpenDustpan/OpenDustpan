package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
)

// mustResolver parses list with ParseList and fails the test on error. The
// resolver reads X-Forwarded-For.
func mustResolver(t *testing.T, list string) *clientip.Resolver {
	t.Helper()

	return mustResolverHeader(t, list, "")
}

// mustResolverHeader parses list and the trusted-proxy-header setting and
// fails the test on error.
func mustResolverHeader(t *testing.T, list, header string) *clientip.Resolver {
	t.Helper()

	prefixes, err := clientip.ParseList(list)
	if err != nil {
		t.Fatalf("parse trusted list %q: %v", list, err)
	}

	name, err := clientip.ParseHeader(header)
	if err != nil {
		t.Fatalf("parse trusted header %q: %v", header, err)
	}

	resolver, err := clientip.NewResolver(prefixes, name)
	if err != nil {
		t.Fatalf("new resolver for %q: %v", list, err)
	}

	return resolver
}

// newRequest returns a request with RemoteAddr set to remote.
func newRequest(t *testing.T, remote string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.invalid/", nil)
	req.RemoteAddr = remote

	return req
}

// addrText renders the Resolve answer: "" for the zero Addr.
func addrText(addr netip.Addr) string {
	if !addr.IsValid() {
		return ""
	}

	return addr.String()
}

// setHeaders adds the X-Forwarded-For and Forwarded lines to req.
func setHeaders(req *http.Request, xff, fwd []string) {
	for _, line := range xff {
		req.Header.Add("X-Forwarded-For", line)
	}

	for _, line := range fwd {
		req.Header.Add("Forwarded", line)
	}
}

// TestResolveIgnoresHeadersFromUntrustedPeer proves headers of a peer
// outside the list never count, and an empty list trusts none.
func TestResolveIgnoresHeadersFromUntrustedPeer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		trusted string
		remote  string
		xff     []string
		fwd     []string
		want    string
	}{
		{
			name:    "spoofed xff from untrusted peer",
			trusted: "10.0.0.0/8",
			remote:  "198.51.100.7:1234",
			xff:     []string{"203.0.113.9"},
			fwd:     nil,
			want:    "198.51.100.7",
		},
		{
			name:    "spoofed forwarded from untrusted peer",
			trusted: "10.0.0.0/8",
			remote:  "198.51.100.7:1234",
			xff:     nil,
			fwd:     []string{"for=203.0.113.9"},
			want:    "198.51.100.7",
		},
		{
			name:    "spoofed both headers from untrusted peer",
			trusted: "10.0.0.0/8",
			remote:  "198.51.100.7:1234",
			xff:     []string{"203.0.113.9"},
			fwd:     []string{"for=203.0.113.9"},
			want:    "198.51.100.7",
		},
		{
			name:    "empty list ignores xff from loopback",
			trusted: "",
			remote:  "127.0.0.1:1234",
			xff:     []string{"203.0.113.9"},
			fwd:     nil,
			want:    "127.0.0.1",
		},
		{
			name:    "empty list ignores forwarded from loopback",
			trusted: "",
			remote:  "127.0.0.1:1234",
			xff:     nil,
			fwd:     []string{"for=203.0.113.9"},
			want:    "127.0.0.1",
		},
		{
			name:    "nil resolver ignores headers",
			trusted: "nil",
			remote:  "10.1.2.3:1234",
			xff:     []string{"203.0.113.9"},
			fwd:     []string{"for=203.0.113.9"},
			want:    "10.1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var resolver *clientip.Resolver
			if tc.trusted != "nil" {
				resolver = mustResolver(t, tc.trusted)
			}

			req := newRequest(t, tc.remote)
			setHeaders(req, tc.xff, tc.fwd)

			if got := addrText(resolver.Resolve(req)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveXFF proves the rightmost-untrusted rule for X-Forwarded-For.
func TestResolveXFF(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		trusted string
		remote  string
		lines   []string
		want    string
	}{
		{
			name:    "client plus trusted proxy",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"203.0.113.9, 10.9.9.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "rightmost untrusted wins",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"1.2.3.4, 203.0.113.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "several header lines join in order",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"203.0.113.9", "10.9.9.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "address with port",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"203.0.113.9:4567, 10.9.9.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "all trusted gives leftmost",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"10.4.4.4, 10.5.5.5"},
			want:    "10.4.4.4",
		},
		{
			name:    "ipv6 client plus trusted proxy",
			trusted: "10.0.0.0/8, 2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			lines:   []string{"198.51.100.7, 2001:db8::8"},
			want:    "198.51.100.7",
		},
		{
			name:    "ipv6 with port",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"[2001:db8::1]:4711, 10.9.9.9"},
			want:    "2001:db8::1",
		},
		{
			name:    "mapped peer unmaps",
			trusted: "",
			remote:  "[::ffff:10.0.0.1]:1234",
			lines:   nil,
			want:    "10.0.0.1",
		},
		{
			name:    "unparseable peer reads unknown",
			trusted: "10.0.0.0/8",
			remote:  "not-an-address",
			lines:   []string{"203.0.113.9"},
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := mustResolver(t, tc.trusted)
			req := newRequest(t, tc.remote)

			for _, line := range tc.lines {
				req.Header.Add("X-Forwarded-For", line)
			}

			if got := addrText(resolver.Resolve(req)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveAdversarialXFF proves a bad header value reads as unknown, never
// as the proxy: the peer 10.0.0.1 is in the trusted list 10.0.0.0/8.
func TestResolveAdversarialXFF(t *testing.T) {
	t.Parallel()

	trusted32 := strings.Repeat("10.9.9.9,", 31) + "10.9.9.9"
	trusted33 := strings.Repeat("10.9.9.9,", 32) + "10.9.9.9"
	trusted39Untrusted := strings.Repeat("10.9.9.9,", 39) + "1.2.3.4"
	trustedV6 := strings.Repeat("2001:db8::9,", 31) + "2001:db8::9"

	cases := []struct {
		name    string
		trusted string
		remote  string
		lines   []string
		want    string
	}{
		{name: "garbage", trusted: "10.0.0.0/8", remote: "10.0.0.1:1234", lines: []string{"not-an-ip"}, want: ""},
		{name: "comma only", trusted: "10.0.0.0/8", remote: "10.0.0.1:1234", lines: []string{","}, want: ""},
		{name: "empty value", trusted: "10.0.0.0/8", remote: "10.0.0.1:1234", lines: []string{""}, want: ""},
		{
			name:    "whitespace value",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{"   "},
			want:    "",
		},
		{
			name:    "32 trusted entries",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{trusted32},
			want:    "",
		},
		{
			name:    "33 trusted entries",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{trusted33},
			want:    "",
		},
		{
			name:    "40 entries with rightmost untrusted",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{trusted39Untrusted},
			want:    "1.2.3.4",
		},
		{
			name:    "left garbage never reached",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{"evil, 1.2.3.4"},
			want:    "1.2.3.4",
		},
		{
			name:    "right garbage ends the walk",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{"1.2.3.4, garbage"},
			want:    "",
		},
		{
			name:    "bad port",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			lines:   []string{"1.2.3.4:99999, 10.9.9.9"},
			want:    "",
		},
		{
			name:    "ipv6 peer with garbage",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			lines:   []string{"not-an-ip"},
			want:    "",
		},
		{
			name:    "ipv6 with 32 trusted entries",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			lines:   []string{trustedV6},
			want:    "",
		},
		{
			name:    "ipv6 all trusted gives leftmost",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			lines:   []string{"2001:db8::5, 2001:db8::6"},
			want:    "2001:db8::5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := mustResolver(t, tc.trusted)
			req := newRequest(t, tc.remote)

			for _, line := range tc.lines {
				req.Header.Add("X-Forwarded-For", line)
			}

			if got := addrText(resolver.Resolve(req)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveForwarded proves the for rule for the Forwarded header. The peer
// 10.1.2.3 is in the trusted list 10.0.0.0/8.
func TestResolveForwarded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "quoted brackets with port", lines: []string{`for="[2001:db8::1]:4711"`}, want: "2001:db8::1"},
		{name: "quoted brackets", lines: []string{`for="[2001:db8::1]"`}, want: "2001:db8::1"},
		{name: "bare ipv4", lines: []string{"for=192.0.2.43"}, want: "192.0.2.43"},
		{name: "bare ipv4 with port", lines: []string{"for=192.0.2.43:4711"}, want: "192.0.2.43"},
		{name: "name case", lines: []string{"FOR=192.0.2.44"}, want: "192.0.2.44"},
		{name: "unknown gives unknown", lines: []string{"for=unknown"}, want: ""},
		{name: "quoted unknown gives unknown", lines: []string{`for="unknown"`}, want: ""},
		{name: "hidden gives unknown", lines: []string{"for=_hidden"}, want: ""},
		{name: "quoted hidden gives unknown", lines: []string{`for="_hidden"`}, want: ""},
		{name: "obfuscated gives unknown", lines: []string{"for=_x"}, want: ""},
		{name: "quoted pair gives unknown", lines: []string{`for="1.1.1.1, 2.2.2.2"`}, want: ""},
		{name: "bad v6 port gives unknown", lines: []string{`for="[2001:db8::1]:abc"`}, want: ""},
		{name: "obfuscated v6 port gives unknown", lines: []string{`for="[2001:db8::1]:_x"`}, want: ""},
		{name: "port out of range gives unknown", lines: []string{"for=1.1.1.1:99999"}, want: ""},
		{name: "unquoted brackets give unknown", lines: []string{"for=[2001:db8::1]"}, want: ""},
		{name: "bare ipv6 gives unknown", lines: []string{"for=2001:db8::1"}, want: ""},
		{
			name:  "pairs and elements",
			lines: []string{"host=example.invalid;proto=http, for=192.0.2.60;host=example.invalid"},
			want:  "192.0.2.60",
		},
		{
			name:  "rightmost non-trusted wins",
			lines: []string{"for=1.2.3.4, for=192.0.2.61"},
			want:  "192.0.2.61",
		},
		{
			name:  "trusted tail skipped",
			lines: []string{"for=192.0.2.62, for=10.9.9.9"},
			want:  "192.0.2.62",
		},
		{
			name:  "several lines join in order",
			lines: []string{"for=192.0.2.63", "for=10.9.9.9"},
			want:  "192.0.2.63",
		},
		{
			name:  "garbage gives unknown",
			lines: []string{"for=not-an-ip"},
			want:  "",
		},
		{
			name:  "no for gives unknown",
			lines: []string{"host=example.invalid"},
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := mustResolverHeader(t, "10.0.0.0/8", "forwarded")
			req := newRequest(t, "10.1.2.3:1234")

			for _, line := range tc.lines {
				req.Header.Add("Forwarded", line)
			}

			if got := addrText(resolver.Resolve(req)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHeaderSetting proves the resolver reads only the named header, and
// that a trusted peer with no line of the named header reads as unknown,
// never as the peer.
func TestHeaderSetting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		header  string
		trusted string
		remote  string
		xff     []string
		fwd     []string
		want    string
	}{
		{
			name:    "forwarded setting reads forwarded",
			header:  "forwarded",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     []string{"6.6.6.6"},
			fwd:     []string{"for=1.2.3.4"},
			want:    "1.2.3.4",
		},
		{
			name:    "x-forwarded-for setting ignores forwarded",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     []string{"10.9.9.9"},
			fwd:     []string{"for=6.6.6.6"},
			want:    "10.9.9.9",
		},
		{
			name:    "forwarded setting with xff only gives unknown",
			header:  "forwarded",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     []string{"1.2.3.4"},
			fwd:     nil,
			want:    "",
		},
		{
			name:    "x-forwarded-for setting with forwarded only gives unknown",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     nil,
			fwd:     []string{"for=1.2.3.4"},
			want:    "",
		},
		{
			name:    "forwarded setting with no header gives unknown",
			header:  "forwarded",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     nil,
			fwd:     nil,
			want:    "",
		},
		{
			name:    "x-forwarded-for setting with no header gives unknown",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     nil,
			fwd:     nil,
			want:    "",
		},
		{
			name:    "ipv6 peer with xff only under forwarded gives unknown",
			header:  "forwarded",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			xff:     []string{"1.2.3.4"},
			fwd:     nil,
			want:    "",
		},
		{
			name:    "ipv6 peer with forwarded only under xff gives unknown",
			header:  "x-forwarded-for",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			xff:     nil,
			fwd:     []string{"for=1.2.3.4"},
			want:    "",
		},
		{
			name:    "ipv6 peer with no header gives unknown",
			header:  "forwarded",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			xff:     nil,
			fwd:     nil,
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := mustResolverHeader(t, tc.trusted, tc.header)
			req := newRequest(t, tc.remote)
			setHeaders(req, tc.xff, tc.fwd)

			if got := addrText(resolver.Resolve(req)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseHeader proves the header setting parses case-insensitively, empty
// means the default, and unknown values fail.
func TestParseHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  clientip.Header
	}{
		{name: "empty means default", value: "", want: clientip.HeaderXForwardedFor},
		{name: "default", value: "x-forwarded-for", want: clientip.HeaderXForwardedFor},
		{name: "forwarded", value: "forwarded", want: clientip.HeaderForwarded},
		{name: "upper case", value: "X-Forwarded-For", want: clientip.HeaderXForwardedFor},
		{name: "mixed case", value: "Forwarded", want: clientip.HeaderForwarded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := clientip.ParseHeader(tc.value)
			if err != nil {
				t.Fatalf("ParseHeader(%q): %v", tc.value, err)
			}

			if got != tc.want {
				t.Fatalf("ParseHeader(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestParseHeaderInvalid proves the error names the bad value.
func TestParseHeaderInvalid(t *testing.T) {
	t.Parallel()

	_, err := clientip.ParseHeader("x-real-ip")
	if err == nil {
		t.Fatal("ParseHeader succeeded, want an error")
	}

	want := `trusted proxy header "x-real-ip" is not x-forwarded-for or forwarded`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestParseListValid proves CIDR items parse.
func TestParseListValid(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("10.0.0.0/8, 2001:db8::/32")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	if len(prefixes) != 2 {
		t.Fatalf("prefixes = %v, want 2 entries", prefixes)
	}

	if prefixes[0].String() != "10.0.0.0/8" || prefixes[1].String() != "2001:db8::/32" {
		t.Fatalf("prefixes = %v, want 10.0.0.0/8 and 2001:db8::/32", prefixes)
	}
}

// TestParseListBareIPs proves bare addresses read as /32 or /128, with
// spaces allowed and empty items skipped.
func TestParseListBareIPs(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList(" 10.0.0.1 ,, 2001:db8::1 ")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	if len(prefixes) != 2 {
		t.Fatalf("prefixes = %v, want 2 entries", prefixes)
	}

	if prefixes[0].String() != "10.0.0.1/32" || prefixes[1].String() != "2001:db8::1/128" {
		t.Fatalf("prefixes = %v, want 10.0.0.1/32 and 2001:db8::1/128", prefixes)
	}
}

// TestParseListValidHosts proves a bare address and a clean network pass.
func TestParseListValidHosts(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("10.0.0.0/8, 10.0.0.1, 2001:db8::/32")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	if len(prefixes) != 3 {
		t.Fatalf("prefixes = %v, want 3 entries", prefixes)
	}
}

// TestParseListUnmaps proves an IPv4-mapped IPv6 item is unmapped first.
func TestParseListUnmaps(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("::ffff:10.0.0.0/104")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	if len(prefixes) != 1 || prefixes[0].String() != "10.0.0.0/8" {
		t.Fatalf("prefixes = %v, want 10.0.0.0/8", prefixes)
	}
}

// TestParseListEmpty proves a blank list parses to no prefixes.
func TestParseListEmpty(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList(" , ")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	if len(prefixes) != 0 {
		t.Fatalf("prefixes = %v, want none", prefixes)
	}
}

// TestParseListInvalid proves the error text names the bad item.
func TestParseListInvalid(t *testing.T) {
	t.Parallel()

	_, err := clientip.ParseList("10.0.0.0/8, not-a-proxy")
	if err == nil {
		t.Fatal("ParseList succeeded, want an error")
	}

	want := `trusted proxy "not-a-proxy" is not a CIDR or an IP address`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestParseListRefused proves a /0 prefix and a prefix with host bits set
// fail with an error that names the item.
func TestParseListRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		list string
		want string
	}{
		{
			name: "ipv4 zero",
			list: "0.0.0.0/0",
			want: `trusted proxy "0.0.0.0/0" covers every address: ` +
				`list the proxies by network or by address`,
		},
		{
			name: "ipv6 zero",
			list: "::/0",
			want: `trusted proxy "::/0" covers every address: ` +
				`list the proxies by network or by address`,
		},
		{
			name: "mapped zero 96",
			list: "::ffff:0:0/96",
			want: `trusted proxy "::ffff:0:0/96" covers every address: ` +
				`list the proxies by network or by address`,
		},
		{
			name: "mapped zero 80",
			list: "::ffff:0:0/80",
			want: `trusted proxy "::ffff:0:0/80" covers every address: ` +
				`list the proxies by network or by address`,
		},
		{
			name: "ipv4 host bits",
			list: "10.0.0.1/8",
			want: `trusted proxy "10.0.0.1/8" has host bits set: ` +
				`write the network (10.0.0.0/8) or one address (/32)`,
		},
		{
			name: "ipv6 host bits",
			list: "2001:db8::1/32",
			want: `trusted proxy "2001:db8::1/32" has host bits set: ` +
				`write the network (2001:db8::/32) or one address (/32)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := clientip.ParseList(tc.list)
			if err == nil {
				t.Fatalf("ParseList(%q) succeeded, want an error", tc.list)
			}

			if err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// TestNewResolverRefused proves NewResolver refuses the same lists as the
// flag, so the SDK option cannot open the server either.
func TestNewResolverRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prefix string
		header clientip.Header
		want   string
	}{
		{
			name:   "zero",
			prefix: "0.0.0.0/0",
			header: clientip.HeaderXForwardedFor,
			want: `trusted proxy "0.0.0.0/0" covers every address: ` +
				`list the proxies by network or by address`,
		},
		{
			name:   "host bits",
			prefix: "10.0.0.1/8",
			header: clientip.HeaderXForwardedFor,
			want: `trusted proxy "10.0.0.1/8" has host bits set: ` +
				`write the network (10.0.0.0/8) or one address (/32)`,
		},
		{
			name:   "ipv6 host bits",
			prefix: "2001:db8::1/32",
			header: clientip.HeaderForwarded,
			want: `trusted proxy "2001:db8::1/32" has host bits set: ` +
				`write the network (2001:db8::/32) or one address (/32)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := clientip.NewResolver([]netip.Prefix{netip.MustParsePrefix(tc.prefix)}, tc.header)
			if err == nil {
				t.Fatalf("NewResolver(%q) succeeded, want an error", tc.prefix)
			}

			if err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// TestNewResolverBadHeader proves an unknown header fails the build.
func TestNewResolverBadHeader(t *testing.T) {
	t.Parallel()

	_, err := clientip.NewResolver(nil, "x-real-ip")
	if err == nil {
		t.Fatal("NewResolver succeeded, want an error")
	}

	want := `trusted proxy header "x-real-ip" is not x-forwarded-for or forwarded`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestMappedPrefixMatchesIPv4 proves a mapped prefix works through
// NewResolver the same as its IPv4 form.
func TestMappedPrefixMatchesIPv4(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("::ffff:10.0.0.0/104")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	resolver, err := clientip.NewResolver(prefixes, clientip.HeaderXForwardedFor)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	req := newRequest(t, "10.1.2.3:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.9.9.9")

	if got := addrText(resolver.Resolve(req)); got != "203.0.113.9" {
		t.Fatalf("Resolve = %q, want 203.0.113.9", got)
	}
}

// TestNewResolverCopiesSlice proves later changes to the input change
// nothing.
func TestNewResolverCopiesSlice(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	resolver, err := clientip.NewResolver(prefixes, clientip.HeaderXForwardedFor)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	prefixes[0] = netip.MustParsePrefix("192.168.0.0/16")

	req := newRequest(t, "10.1.2.3:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.9.9.9")

	if got := addrText(resolver.Resolve(req)); got != "203.0.113.9" {
		t.Fatalf("Resolve = %q, want 203.0.113.9", got)
	}
}

// TestTrustedPeerWithoutHeader proves when a trusted peer sent no line of
// the named header.
func TestTrustedPeerWithoutHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		header  string
		trusted string
		remote  string
		xff     []string
		fwd     []string
		want    bool
	}{
		{
			name:    "trusted peer with no header",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     nil,
			fwd:     nil,
			want:    true,
		},
		{
			name:    "trusted peer with only the other header",
			header:  "forwarded",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     []string{"1.2.3.4"},
			fwd:     nil,
			want:    true,
		},
		{
			name:    "trusted peer with the named header",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "10.0.0.1:1234",
			xff:     []string{"1.2.3.4"},
			fwd:     nil,
			want:    false,
		},
		{
			name:    "untrusted peer with no header",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "198.51.100.7:1234",
			xff:     nil,
			fwd:     nil,
			want:    false,
		},
		{
			name:    "ipv6 trusted peer with no header",
			header:  "forwarded",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			xff:     nil,
			fwd:     nil,
			want:    true,
		},
		{
			name:    "ipv6 trusted peer with the named header",
			header:  "forwarded",
			trusted: "2001:db8::/32",
			remote:  "[2001:db8::99]:1234",
			xff:     nil,
			fwd:     []string{"for=1.2.3.4"},
			want:    false,
		},
		{
			name:    "unparseable peer",
			header:  "x-forwarded-for",
			trusted: "10.0.0.0/8",
			remote:  "not-an-address",
			xff:     nil,
			fwd:     nil,
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := mustResolverHeader(t, tc.trusted, tc.header)
			req := newRequest(t, tc.remote)
			setHeaders(req, tc.xff, tc.fwd)

			if got := resolver.TrustedPeerWithoutHeader(req); got != tc.want {
				t.Fatalf("TrustedPeerWithoutHeader = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTrustedPeerWithoutHeaderNil proves a nil request never warns.
func TestTrustedPeerWithoutHeaderNil(t *testing.T) {
	t.Parallel()

	resolver := mustResolver(t, "10.0.0.0/8")

	if resolver.TrustedPeerWithoutHeader(nil) {
		t.Fatal("TrustedPeerWithoutHeader(nil) = true, want false")
	}
}

// TestPeer proves Peer returns the TCP peer and never reads a header.
func TestPeer(t *testing.T) {
	t.Parallel()

	req := newRequest(t, "198.51.100.7:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	if got := clientip.Peer(req); got != netip.MustParseAddr("198.51.100.7") {
		t.Fatalf("Peer = %v, want 198.51.100.7", got)
	}

	broken := newRequest(t, "not-an-address")
	if got := clientip.Peer(broken); got.IsValid() {
		t.Fatalf("Peer = %v, want unknown", got)
	}

	if got := clientip.Peer(nil); got.IsValid() {
		t.Fatalf("Peer(nil) = %v, want unknown", got)
	}
}

// TestOfWithoutContext proves Of falls back to the RemoteAddr peer and never
// reads headers.
func TestOfWithoutContext(t *testing.T) {
	t.Parallel()

	req := newRequest(t, "198.51.100.7:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("Forwarded", "for=203.0.113.9")

	if got := addrText(clientip.Of(req)); got != "198.51.100.7" {
		t.Fatalf("Of = %q, want 198.51.100.7", got)
	}

	if got := clientip.String(req); got != "198.51.100.7" {
		t.Fatalf("String = %q, want 198.51.100.7", got)
	}
}

// TestWithAddrRoundTrip proves WithAddr then Of returns the stored address.
func TestWithAddrRoundTrip(t *testing.T) {
	t.Parallel()

	req := newRequest(t, "10.1.2.3:1234")
	stored := netip.MustParseAddr("203.0.113.9")
	req = req.WithContext(clientip.WithAddr(req.Context(), stored))

	if got := clientip.Of(req); got != stored {
		t.Fatalf("Of = %v, want %v", got, stored)
	}

	if got := clientip.String(req); got != "203.0.113.9" {
		t.Fatalf("String = %q, want 203.0.113.9", got)
	}
}

// TestStringUnknown proves String returns "" for an unknown address, never
// the raw RemoteAddr.
func TestStringUnknown(t *testing.T) {
	t.Parallel()

	broken := newRequest(t, "not-an-address")
	if got := clientip.String(broken); got != "" {
		t.Fatalf("String = %q, want empty", got)
	}

	unknown := newRequest(t, "not-an-address")
	unknown = unknown.WithContext(clientip.WithAddr(unknown.Context(), netip.Addr{}))

	if got := clientip.String(unknown); got != "" {
		t.Fatalf("String = %q, want empty", got)
	}

	if got := clientip.String(nil); got != "" {
		t.Fatalf("String(nil) = %q, want empty", got)
	}
}
