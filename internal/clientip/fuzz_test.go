package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
)

// FuzzParseHeader checks that ParseHeader answers only one of the two
// headers or an error, and that the answer parses back to itself.
func FuzzParseHeader(f *testing.F) {
	for _, s := range []string{"", " ", "x-forwarded-for", "X-Forwarded-For", " forwarded ", "FORWARDED", "x-real-ip"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		header, err := clientip.ParseHeader(s)
		if err != nil {
			if header != "" {
				t.Fatalf("ParseHeader(%q) = %q with error %v, want an empty header", s, header, err)
			}

			return
		}

		if header != clientip.HeaderXForwardedFor && header != clientip.HeaderForwarded {
			t.Fatalf("ParseHeader(%q) = %q, want one of the two headers", s, header)
		}

		again, err := clientip.ParseHeader(string(header))
		if err != nil || again != header {
			t.Fatalf("ParseHeader(%q) = %q, %v, want %q", header, again, err, header)
		}
	})
}

// FuzzParseList checks the invariants of each list that ParseList takes: no
// prefix covers every address, no prefix has host bits, no prefix is an
// IPv4-mapped IPv6 prefix or has a zone, NewResolver takes the list, and the
// text of the list parses back to the same list.
func FuzzParseList(f *testing.F) {
	for _, s := range []string{
		"", "10.0.0.0/8", "10.0.0.1", " 10.0.0.0/8 , ,2001:db8::/32", "::ffff:10.0.0.0/104", "fe80::1%eth0",
		"0.0.0.0/0", "::/0", "::ffff:0:0/80", "10.0.0.1/8", "not-an-address", "10.0.0.0/33",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		list, err := clientip.ParseList(s)
		if err != nil {
			if list != nil {
				t.Fatalf("ParseList(%q) = %v with error %v, want no list", s, list, err)
			}

			return
		}

		texts := checkTrustedList(t, s, list)

		if _, resolverErr := clientip.NewResolver(list, ""); resolverErr != nil {
			t.Fatalf("NewResolver refuses the list %v of ParseList(%q): %v", list, s, resolverErr)
		}

		back, err := clientip.ParseList(strings.Join(texts, ","))
		if err != nil || !slices.Equal(back, list) {
			t.Fatalf("ParseList(%q) = %v, %v, want %v", strings.Join(texts, ","), back, err, list)
		}
	})
}

// FuzzResolve checks Resolve with X-Forwarded-For and with Forwarded:
//   - the answer is the zero Addr or an address with no zone that is not
//     IPv4-mapped;
//   - a peer outside the trusted list reads as the peer, whatever the header
//     holds;
//   - an address that the walk finds outside the trusted list does not change
//     when a client prepends a header line or an element before it.
func FuzzResolve(f *testing.F) {
	f.Add("10.0.0.0/8", false, "10.0.0.1:443", "203.0.113.9, 10.0.0.2", "198.51.100.7")
	f.Add("10.0.0.0/8", false, "203.0.113.1:443", "198.51.100.7", "")
	f.Add("10.0.0.0/8,2001:db8::/32", false, "[2001:db8::1]:443", "[2001:db8::2]:80, 192.0.2.4:8080", "")
	f.Add("10.0.0.0/8", true, "10.0.0.1:443", `for=192.0.2.60;proto=http;by=203.0.113.43`, `for="[2001:db8::7]:4711"`)
	f.Add("10.0.0.0/8", true, "10.0.0.1:443", `for="_hidden", for=unknown`, `for="10.0.0.3\"`)
	f.Add("10.0.0.0/8", true, "10.0.0.1", `For=10.0.0.4;for=192.0.2.1`, `for="192.0.2.1:99999"`)
	f.Add("", false, "not-an-address", "", "")

	f.Fuzz(func(t *testing.T, list string, forwarded bool, remote, first, second string) {
		prefixes, err := clientip.ParseList(list)
		if err != nil {
			return
		}

		header, name, prefix := clientip.HeaderXForwardedFor, "X-Forwarded-For", "192.0.2.200"
		if forwarded {
			header, name, prefix = clientip.HeaderForwarded, "Forwarded", "for=192.0.2.200"
		}

		resolver, err := clientip.NewResolver(prefixes, header)
		if err != nil {
			t.Fatalf("NewResolver(%v, %q): %v", prefixes, header, err)
		}

		in := fuzzInput{remote: remote, name: name, prefix: prefix, lines: headerLines(first, second)}
		checkResolve(t, resolver, prefixes, in)
	})
}

// checkTrustedList fails t for a prefix of list that ParseList(s) must not
// answer, and returns the text of each prefix.
func checkTrustedList(t *testing.T, s string, list []netip.Prefix) []string {
	t.Helper()

	texts := make([]string, 0, len(list))

	for _, prefix := range list {
		addr := prefix.Addr()

		switch {
		case !prefix.IsValid():
			t.Fatalf("ParseList(%q) holds the prefix %v, which is not valid", s, prefix)
		case prefix.Bits() == 0:
			t.Fatalf("ParseList(%q) holds the prefix %v, which covers every address", s, prefix)
		case prefix != prefix.Masked():
			t.Fatalf("ParseList(%q) holds the prefix %v, which has host bits", s, prefix)
		case addr.Is4In6():
			t.Fatalf("ParseList(%q) holds the prefix %v, which is IPv4-mapped", s, prefix)
		case addr.Zone() != "":
			t.Fatalf("ParseList(%q) holds the prefix %v, which has a zone", s, prefix)
		default:
		}

		texts = append(texts, prefix.String())
	}

	return texts
}

// fuzzInput is one request of FuzzResolve: the RemoteAddr, the header name,
// the line that a client prepends, and the lines of the header.
type fuzzInput struct {
	remote string
	name   string
	prefix string
	lines  []string
}

// checkResolve fails t when the answer of resolver for in breaks an invariant
// of FuzzResolve.
func checkResolve(t *testing.T, resolver *clientip.Resolver, prefixes []netip.Prefix, in fuzzInput) {
	t.Helper()

	got := resolver.Resolve(fuzzRequest(t, in.remote, in.name, in.lines))

	if got.IsValid() && (got.Zone() != "" || got.Is4In6()) {
		t.Fatalf("Resolve(%q, %q) = %v, want no zone and no IPv4-mapped form", in.remote, in.lines, got)
	}

	peer := clientip.Peer(fuzzRequest(t, in.remote, in.name, nil))
	if peer.IsValid() && !inPrefixes(prefixes, peer) && got != peer {
		t.Fatalf("Resolve(%q, %q) = %v, want the untrusted peer %v", in.remote, in.lines, got, peer)
	}

	if !got.IsValid() || inPrefixes(prefixes, got) {
		return
	}

	withLine := append([]string{in.prefix}, in.lines...)
	if again := resolver.Resolve(fuzzRequest(t, in.remote, in.name, withLine)); again != got {
		t.Fatalf("Resolve(%q, %q) = %v after a prepended line, want %v", in.remote, withLine, again, got)
	}
}

// headerLines returns the non-empty header lines of a fuzz input in order.
func headerLines(lines ...string) []string {
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}

	return out
}

// fuzzRequest returns a request from remote with each line as one line of
// the header name.
func fuzzRequest(t *testing.T, remote, name string, lines []string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.invalid/", nil)
	req.RemoteAddr = remote

	for _, line := range lines {
		req.Header.Add(name, line)
	}

	return req
}

// inPrefixes reports whether one of prefixes contains addr.
func inPrefixes(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}
