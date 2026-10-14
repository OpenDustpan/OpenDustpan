package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
)

// mustResolver parses list with ParseList and fails the test on error.
func mustResolver(t *testing.T, list string) *clientip.Resolver {
	t.Helper()

	prefixes, err := clientip.ParseList(list)
	if err != nil {
		t.Fatalf("parse trusted list %q: %v", list, err)
	}

	return clientip.NewResolver(prefixes)
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

			for _, line := range tc.xff {
				req.Header.Add("X-Forwarded-For", line)
			}

			for _, line := range tc.fwd {
				req.Header.Add("Forwarded", line)
			}

			if got := addrText(resolver.Resolve(req)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveXFF proves the rightmost-untrusted rule for X-Forwarded-For.
func TestResolveXFF(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("10.9.9.9,", 32) + "10.9.9.9"

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
			name:    "prepended attacker value loses",
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
			name:    "garbage gives peer",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"203.0.113.9, not-an-ip"},
			want:    "10.1.2.3",
		},
		{
			name:    "empty element gives peer",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{"203.0.113.9,, 10.9.9.9"},
			want:    "10.1.2.3",
		},
		{
			name:    "33 elements give peer",
			trusted: "10.0.0.0/8",
			remote:  "10.1.2.3:1234",
			lines:   []string{long},
			want:    "10.1.2.3",
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

// TestResolveForwarded proves the for rule for the Forwarded header.
func TestResolveForwarded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "quoted brackets with port", lines: []string{`for="[2001:db8::1]:4711"`}, want: "2001:db8::1"},
		{name: "bare ipv4", lines: []string{"for=192.0.2.43"}, want: "192.0.2.43"},
		{name: "name case", lines: []string{"FOR=192.0.2.44"}, want: "192.0.2.44"},
		{name: "unknown gives peer", lines: []string{"for=unknown"}, want: "10.1.2.3"},
		{name: "quoted unknown gives peer", lines: []string{`for="unknown"`}, want: "10.1.2.3"},
		{name: "hidden gives peer", lines: []string{"for=_hidden"}, want: "10.1.2.3"},
		{name: "quoted hidden gives peer", lines: []string{`for="_hidden"`}, want: "10.1.2.3"},
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
			name:  "garbage gives peer",
			lines: []string{"for=not-an-ip"},
			want:  "10.1.2.3",
		},
		{
			name:  "no for gives peer",
			lines: []string{"host=example.invalid"},
			want:  "10.1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := mustResolver(t, "10.0.0.0/8")
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

// TestXFFWinsOverForwarded proves X-Forwarded-For wins when both headers
// are present.
func TestXFFWinsOverForwarded(t *testing.T) {
	t.Parallel()

	resolver := mustResolver(t, "10.0.0.0/8")
	req := newRequest(t, "10.1.2.3:1234")
	req.Header.Set("X-Forwarded-For", "192.0.2.70")
	req.Header.Set("Forwarded", "for=192.0.2.71")

	if got := addrText(resolver.Resolve(req)); got != "192.0.2.70" {
		t.Fatalf("Resolve = %q, want 192.0.2.70", got)
	}
}

// TestTrustedPeerWithoutHeaders proves a trusted peer with no headers reads
// as itself.
func TestTrustedPeerWithoutHeaders(t *testing.T) {
	t.Parallel()

	resolver := mustResolver(t, "10.0.0.0/8")
	req := newRequest(t, "10.1.2.3:1234")

	if got := addrText(resolver.Resolve(req)); got != "10.1.2.3" {
		t.Fatalf("Resolve = %q, want 10.1.2.3", got)
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

// TestParseListMasksAndUnmaps proves a CIDR is masked and an IPv4-mapped
// IPv6 item is unmapped first.
func TestParseListMasksAndUnmaps(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("10.1.2.3/8, ::ffff:10.0.0.0/104")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	if len(prefixes) != 2 {
		t.Fatalf("prefixes = %v, want 2 entries", prefixes)
	}

	if prefixes[0].String() != "10.0.0.0/8" || prefixes[1].String() != "10.0.0.0/8" {
		t.Fatalf("prefixes = %v, want masked and unmapped 10.0.0.0/8 twice", prefixes)
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

// TestNewResolverCopiesSlice proves later changes to the input change
// nothing.
func TestNewResolverCopiesSlice(t *testing.T) {
	t.Parallel()

	prefixes, err := clientip.ParseList("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}

	resolver := clientip.NewResolver(prefixes)
	prefixes[0] = netip.MustParsePrefix("192.168.0.0/16")

	req := newRequest(t, "10.1.2.3:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.9.9.9")

	if got := addrText(resolver.Resolve(req)); got != "203.0.113.9" {
		t.Fatalf("Resolve = %q, want 203.0.113.9", got)
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

// TestStringFallback proves String strips the port of an unknown address and
// returns an unparseable RemoteAddr unchanged.
func TestStringFallback(t *testing.T) {
	t.Parallel()

	plain := newRequest(t, "198.51.100.7:1234")
	if got := clientip.String(plain); got != "198.51.100.7" {
		t.Fatalf("String = %q, want 198.51.100.7", got)
	}

	bare := newRequest(t, "198.51.100.7")
	if got := clientip.String(bare); got != "198.51.100.7" {
		t.Fatalf("String = %q, want 198.51.100.7", got)
	}

	broken := newRequest(t, "not-an-address")
	if got := clientip.String(broken); got != "not-an-address" {
		t.Fatalf("String = %q, want the value unchanged", got)
	}

	unknown := newRequest(t, "not-an-address")
	unknown = unknown.WithContext(clientip.WithAddr(unknown.Context(), netip.Addr{}))

	if got := clientip.String(unknown); got != "not-an-address" {
		t.Fatalf("String = %q, want the value unchanged", got)
	}
}
