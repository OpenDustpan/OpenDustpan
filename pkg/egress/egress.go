// Package egress holds the rules for the outbound requests that the server
// makes to an address that a principal gives: the delivery of a
// notification to a webhook and the read of an iCal feed. A principal that
// can save a URL must not reach the network of the server through it. The
// rules apply to the URL when a principal saves it or tests it, and to the
// resolved IP address of every connection, so a DNS answer that changes
// after the check (DNS rebinding) cannot pass them.
//
// The adapter in egresshttp applies the rules to each connection. Every
// sender and every reader of a URL from a principal uses that adapter.
package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const https = "https"

// ErrRefused is the error for a URL or an address that the rules refuse.
// A status that the server stores for a principal holds this class only,
// never the address or the text of the network error.
var ErrRefused = errors.New("the address is not allowed for an outbound request")

// Bounds of an outbound request.
const (
	// MaxBodyBytes is the largest body that the server reads from a
	// response, after decompression.
	MaxBodyBytes = 4 << 20
	// Timeout is the longest time that one request, with its connection,
	// its redirects and its body, takes.
	Timeout = 30 * time.Second
	// MaxURLBytes is the longest URL that a principal can save.
	MaxURLBytes = 2048
)

// Scheme is the set of URL schemes that a use of the rules accepts.
type Scheme int

const (
	// HTTPSOnly accepts https URLs only, for example a webhook.
	HTTPSOnly Scheme = iota
	// HTTPOrHTTPS also accepts http URLs, for example a public iCal feed
	// that has no TLS.
	HTTPOrHTTPS
)

// blocked returns the address ranges that the rules refuse, in addition to
// the classes that netip reports (loopback, private, link-local,
// unspecified, multicast).
func blocked() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),      // this network
		netip.MustParsePrefix("100.64.0.0/10"),  // shared address space (carrier-grade NAT)
		netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
		netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
		netip.MustParsePrefix("240.0.0.0/4"),    // reserved, and the limited broadcast
		netip.MustParsePrefix("fc00::/7"),       // unique local
		netip.MustParsePrefix("64:ff9b::/96"),   // NAT64
		netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
		netip.MustParsePrefix("2002::/16"),      // 6to4, which embeds an IPv4 address
		netip.MustParsePrefix("2001::/32"),      // Teredo, which embeds an IPv4 address
		netip.MustParsePrefix("100::/64"),       // discard
		netip.MustParsePrefix("fec0::/10"),      // deprecated site-local
	}
}

// CheckAddr returns ErrRefused for an address that is not a public unicast
// address: loopback, private (RFC 1918), link-local, unspecified,
// multicast, shared (100.64.0.0/10), unique local (fc00::/7), NAT64, an
// address with an embedded IPv4 address, or another reserved range. It
// unmaps an IPv4-mapped IPv6 address and checks the IPv4 address.
func CheckAddr(a netip.Addr) error {
	a = a.Unmap()

	switch {
	case !a.IsValid(),
		a.IsLoopback(),
		a.IsPrivate(),
		a.IsLinkLocalUnicast(),
		a.IsLinkLocalMulticast(),
		a.IsInterfaceLocalMulticast(),
		a.IsMulticast(),
		a.IsUnspecified():
		return fmt.Errorf("%w: %s is not a public address", ErrRefused, a)
	}

	for _, p := range blocked() {
		if p.Contains(a) {
			return fmt.Errorf("%w: %s is in the reserved range %s", ErrRefused, a, p)
		}
	}

	return nil
}

// CheckURL returns ErrRefused for a URL that the rules refuse: a URL that
// does not parse, that is longer than MaxURLBytes, that has a scheme
// outside s, no host, user information, or a fragment, or whose host is an
// IP address that CheckAddr refuses, or the name localhost. A host name
// passes: the adapter checks the resolved address of each connection.
func CheckURL(raw string, s Scheme) error {
	if len(raw) > MaxURLBytes {
		return fmt.Errorf("%w: the URL is longer than %d bytes", ErrRefused, MaxURLBytes)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: the URL does not parse", ErrRefused)
	}

	switch {
	case u.Scheme == https:
	case u.Scheme == "http" && s == HTTPOrHTTPS:
	default:
		return fmt.Errorf("%w: the scheme %q is not allowed", ErrRefused, u.Scheme)
	}

	switch {
	case u.User != nil:
		return fmt.Errorf("%w: the URL has user information", ErrRefused)
	case u.Fragment != "":
		return fmt.Errorf("%w: the URL has a fragment", ErrRefused)
	case u.Hostname() == "":
		return fmt.Errorf("%w: the URL has no host", ErrRefused)
	}

	return checkHost(u.Hostname())
}

func checkHost(host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: the host is localhost", ErrRefused)
	}

	if a, err := netip.ParseAddr(host); err == nil {
		return CheckAddr(a)
	}

	return nil
}

// CheckRedirect returns ErrRefused for a redirect from the URL from to the
// URL to that the rules refuse. A use with SameHost follows a redirect
// only to the same host and never from https to http; a use with NoRedirect
// follows none. Every redirect target also passes CheckURL.
func CheckRedirect(from, to *url.URL, r Redirects, s Scheme) error {
	if r == NoRedirect {
		return fmt.Errorf("%w: the response is a redirect", ErrRefused)
	}

	if from == nil || to == nil {
		return fmt.Errorf("%w: the redirect has no URL", ErrRefused)
	}

	if err := CheckURL(to.String(), s); err != nil {
		return err
	}

	switch {
	case !strings.EqualFold(from.Hostname(), to.Hostname()):
		return fmt.Errorf("%w: the redirect goes to another host", ErrRefused)
	case from.Scheme == https && to.Scheme != https:
		return fmt.Errorf("%w: the redirect goes from https to http", ErrRefused)
	default:
		return nil
	}
}

// Redirects is the redirect policy of a use of the rules.
type Redirects int

const (
	// NoRedirect follows no redirect, for example a webhook delivery.
	NoRedirect Redirects = iota
	// SameHost follows a redirect only to the same host, for example an
	// iCal feed.
	SameHost
)

// MaxRedirects is the largest number of redirects that a use with
// SameHost follows.
const MaxRedirects = 3
