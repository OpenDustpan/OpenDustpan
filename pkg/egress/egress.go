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
	"slices"
	"strconv"
	"strings"
	"time"
)

const https = "https"

// ipv4MappedBits is the length of the prefix of an IPv4-mapped IPv6 address.
const ipv4MappedBits = 96

// ErrRange is the error for a range that ParseRanges does not accept.
var ErrRange = errors.New("the range is not a CIDR range such as 10.0.0.0/8")

// ErrRangeTooWide is the error for a range that ParseAllowedRanges does not
// accept because it is too wide or because it is not a private range.
var ErrRangeTooWide = errors.New("the range is too wide or is not a private range")

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
	// HTTPOrHTTPS also accepts http URLs. No use in the server accepts
	// http now: the webhook and the iCal feed both use HTTPSOnly.
	HTTPOrHTTPS
)

// blocked returns the address ranges that the rules refuse, in addition to
// the classes that netip reports (loopback, private, link-local,
// unspecified, multicast).
func blocked() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),        // this network
		netip.MustParsePrefix("100.64.0.0/10"),    // shared address space (carrier-grade NAT)
		netip.MustParsePrefix("192.0.0.0/24"),     // IETF protocol assignments
		netip.MustParsePrefix("198.18.0.0/15"),    // benchmarking
		netip.MustParsePrefix("240.0.0.0/4"),      // reserved, and the limited broadcast
		netip.MustParsePrefix("fc00::/7"),         // unique local
		netip.MustParsePrefix("64:ff9b::/96"),     // NAT64
		netip.MustParsePrefix("64:ff9b:1::/48"),   // local-use NAT64
		netip.MustParsePrefix("2002::/16"),        // 6to4, which embeds an IPv4 address
		netip.MustParsePrefix("2001::/32"),        // Teredo, which embeds an IPv4 address
		netip.MustParsePrefix("100::/64"),         // discard
		netip.MustParsePrefix("fec0::/10"),        // deprecated site-local
		netip.MustParsePrefix("::/96"),            // IPv4-compatible, which embeds an IPv4 address
		netip.MustParsePrefix("::ffff:0:0:0/96"),  // IPv4-translated, which embeds an IPv4 address
		netip.MustParsePrefix("192.0.2.0/24"),     // documentation (TEST-NET-1)
		netip.MustParsePrefix("198.51.100.0/24"),  // documentation (TEST-NET-2)
		netip.MustParsePrefix("203.0.113.0/24"),   // documentation (TEST-NET-3)
		netip.MustParsePrefix("2001:db8::/32"),    // documentation
		netip.MustParsePrefix("168.63.129.16/32"), // Azure wire server, a metadata service on a public address
	}
}

// Rules are the egress rules with the address ranges that an operator
// allows. The zero value allows no range: it applies the rules as they
// are. An allowed range lets a private-network address pass the address
// checks (RFC 1918 and unique local addresses), for example a GitHub
// Enterprise Server on a private network. It never opens loopback,
// link-local, cloud metadata, unspecified or multicast addresses, and it
// never lets a URL pass that fails the checks of its shape: scheme, user
// information, fragment, and a host that is a number.
type Rules struct {
	// Allow lists the ranges that pass the address checks for private-network
	// addresses.
	Allow []netip.Prefix
	// Loopback lets the loopback addresses pass. It exists for the tests of an
	// adapter that run a fake server on the local host. No flag, no
	// environment variable and no setting of an operator reaches it.
	Loopback bool
}

// Bounds of an allowed range. A range with fewer bits opens more of the
// network than an operator needs to name.
const (
	// MinAllowedBits4 is the shortest prefix of an allowed IPv4 range.
	MinAllowedBits4 = 8
	// MinAllowedBits6 is the shortest prefix of an allowed IPv6 range.
	MinAllowedBits6 = 32
)

// ParseAllowedRanges parses the ranges that an operator allows with
// ParseRanges, and refuses a range that is too wide: an IPv4 prefix shorter
// than MinAllowedBits4, an IPv6 prefix shorter than MinAllowedBits6 (this
// includes 0.0.0.0/0 and ::/0), and a range that starts in the loopback,
// link-local, unspecified or multicast addresses. A range never opens
// those addresses: CheckAddr refuses them also inside a range.
func ParseAllowedRanges(list []string) ([]netip.Prefix, error) {
	ranges, err := ParseRanges(list)
	if err != nil {
		return nil, err
	}

	for _, p := range ranges {
		if err := CheckAllowedRange(p); err != nil {
			return nil, err
		}
	}

	return ranges, nil
}

// CheckAllowedRange returns ErrRangeTooWide for a range that an operator
// must not allow. See ParseAllowedRanges.
func CheckAllowedRange(p netip.Prefix) error {
	a := p.Addr().Unmap()
	shortest := MinAllowedBits6

	if a.Is4() {
		shortest = MinAllowedBits4
	}

	switch {
	case !p.IsValid():
		return fmt.Errorf("%w: %v", ErrRangeTooWide, p)
	case unmappedBits(p) < shortest:
		return fmt.Errorf("%w: %v has fewer than %d bits", ErrRangeTooWide, p, shortest)
	case neverOpened(a):
		return fmt.Errorf("%w: %v starts in an address that no range opens", ErrRangeTooWide, p)
	}

	return nil
}

// neverOpened reports whether an allowed range must never open the address
// a: loopback, link-local, unspecified, multicast, and the cloud metadata
// addresses.
func neverOpened(a netip.Addr) bool {
	return !a.IsValid() ||
		a.IsLoopback() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() ||
		a.IsMulticast() ||
		a.IsUnspecified() ||
		slices.Contains(metadataAddrs(), a)
}

// metadataAddrs returns the cloud metadata addresses. Most of them are
// outside the private ranges already. Defense in depth: fd00:ec2::254 is
// inside the unique local range, which an allowed range may open.
func metadataAddrs() []netip.Addr {
	return []netip.Addr{
		netip.MustParseAddr("169.254.169.254"),
		netip.MustParseAddr("169.254.170.2"),
		netip.MustParseAddr("168.63.129.16"),
		netip.MustParseAddr("100.100.100.200"),
		netip.MustParseAddr("192.0.0.192"),
		netip.MustParseAddr("fd00:ec2::254"),
	}
}

// ParseRanges parses a list of CIDR ranges, for example "10.0.0.0/8". It
// returns an error for an empty entry and for an entry that is not a
// range. The result holds masked prefixes.
func ParseRanges(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))

	for _, raw := range list {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrRange, raw)
		}

		out = append(out, netip.PrefixFrom(p.Addr().Unmap(), unmappedBits(p)).Masked())
	}

	return out, nil
}

// unmappedBits returns the prefix length of p after an IPv4-mapped IPv6
// address becomes an IPv4 address.
func unmappedBits(p netip.Prefix) int {
	if p.Addr().Is4In6() && p.Bits() >= ipv4MappedBits {
		return p.Bits() - ipv4MappedBits
	}

	return p.Bits()
}

// CheckAddr returns ErrRefused for an address that is not a public unicast
// address and that no allowed range opens. A range opens private-network
// addresses only (RFC 1918 and unique local). It never opens loopback,
// link-local, unspecified, multicast, a cloud metadata address, shared
// (100.64.0.0/10), NAT64, an address with an embedded IPv4 address, or
// another reserved range. It unmaps an IPv4-mapped IPv6 address and checks
// the IPv4 address.
func (r Rules) CheckAddr(a netip.Addr) error {
	a = a.Unmap()

	// An allowed range opens only a private-network address. Defense in
	// depth: CheckAddr below refuses the other addresses, and neverOpened
	// refuses its classes here too, so a change in one does not open them.
	if a.IsValid() && a.IsPrivate() && !neverOpened(a) && r.allowed(a) {
		return nil
	}

	if r.Loopback && a.IsLoopback() {
		return nil
	}

	return CheckAddr(a)
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
// IP address that CheckAddr refuses, the name localhost, or a host whose
// labels are all decimal or 0x-hexadecimal numbers (for example 127.1,
// 2130706433 or 0x7f000001), which some resolvers read as an IPv4
// address. A host name passes: the adapter checks the resolved address of
// each connection.
func CheckURL(raw string, s Scheme) error {
	return Rules{Allow: nil, Loopback: false}.CheckURL(raw, s)
}

// CheckURL is the package function CheckURL with the allowed ranges of r.
// A host that is an IP address in an allowed range passes. The name
// localhost and a host that is a number never pass.
func (r Rules) CheckURL(raw string, s Scheme) error {
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

	return r.checkHost(u.Hostname())
}

// numericLabel reports whether l is a decimal number or a number with the
// prefix 0x, which a resolver can read as a part of an IPv4 address.
func numericLabel(l string) bool {
	digits := "0123456789"

	if rest, ok := strings.CutPrefix(l, "0x"); ok {
		l, digits = rest, "0123456789abcdef"

		if l == "" {
			return true
		}
	}

	return l != "" && strings.Trim(l, digits) == ""
}

// CheckRedirect returns ErrRefused for a redirect from the URL from to the
// URL to that the rules refuse. A use with SameHost follows a redirect
// only to the same host and never from https to http; a use with NoRedirect
// follows none. Every redirect target also passes CheckURL.
func CheckRedirect(from, to *url.URL, r Redirects, s Scheme) error {
	return Rules{Allow: nil, Loopback: false}.CheckRedirect(from, to, r, s)
}

// CheckRedirect is the package function CheckRedirect with the allowed
// ranges of r.
func (r Rules) CheckRedirect(from, to *url.URL, redirects Redirects, s Scheme) error {
	if redirects == NoRedirect {
		return fmt.Errorf("%w: the response is a redirect", ErrRefused)
	}

	if from == nil || to == nil {
		return fmt.Errorf("%w: the redirect has no URL", ErrRefused)
	}

	if err := r.CheckURL(to.String(), s); err != nil {
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

// allowed reports whether the address a is in an allowed range.
func (r Rules) allowed(a netip.Addr) bool {
	return slices.ContainsFunc(r.Allow, func(p netip.Prefix) bool { return p.Contains(a) })
}

func (r Rules) checkHost(host string) error {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: the host is localhost", ErrRefused)
	}

	if a, err := netip.ParseAddr(host); err == nil {
		return r.CheckAddr(a)
	}

	if !slices.ContainsFunc(strings.Split(host, "."), func(l string) bool { return !numericLabel(l) }) {
		return fmt.Errorf("%w: the host is a number, not a name", ErrRefused)
	}

	return nil
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

// DefaultPort is the one port that a request may use when the operator
// allows no other: the port of https.
const DefaultPort = 443

// maxPort is the highest TCP port.
const maxPort = 65535

// Ports is the set of destination ports that an outbound request may use.
// The zero value allows DefaultPort only. A server with a port outside this
// set would let a principal who can save a URL scan the ports of a public
// host through the answers of a test.
type Ports struct {
	extra []int
}

// NewPorts returns the set of DefaultPort and the ports extra. It returns
// ErrRefused for a port outside 1 to 65535.
func NewPorts(extra ...int) (Ports, error) {
	for _, port := range extra {
		if port < 1 || port > maxPort {
			return Ports{extra: nil}, fmt.Errorf("%w: %d is not a port number", ErrRefused, port)
		}
	}

	return Ports{extra: slices.Clone(extra)}, nil
}

// Allows reports whether port is in the set.
func (p Ports) Allows(port int) bool {
	return port == DefaultPort || slices.Contains(p.extra, port)
}

// CheckPort returns ErrRefused when the port of the URL raw is not in p. A
// URL with no port has DefaultPort, because the rules accept the https scheme
// only. CheckPort does not check the rest of the URL: CheckURL does.
func CheckPort(raw string, p Ports) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: the URL does not parse", ErrRefused)
	}

	port := DefaultPort

	if text := u.Port(); text != "" {
		port, err = strconv.Atoi(text)
		if err != nil {
			return fmt.Errorf("%w: the port is not a number", ErrRefused)
		}
	}

	if !p.Allows(port) {
		return fmt.Errorf("%w: the port %d is not allowed", ErrRefused, port)
	}

	return nil
}
