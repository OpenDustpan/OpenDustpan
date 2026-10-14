// Package clientip answers the client address of a request.
//
// The server writes the address to the request log and to the source of each
// audit entry. By default the address is the TCP peer only. When the operator
// names trusted proxies, the server also reads the X-Forwarded-For and
// Forwarded headers of a request from a trusted peer.
//
// A client can write any value into these headers. The resolver walks the
// header from the right and takes the first address outside the trusted list.
// A value that a client prepends never wins over the value that a trusted
// proxy added. When X-Forwarded-For is present it wins over Forwarded.
// Headers of a peer outside the list never count.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// maxForwardedHops caps the entries of one header walk. A longer list reads
// as the peer: no client can make the walk grow without a bound.
const maxForwardedHops = 32

// ipv4Bits is the prefix length of one bare IPv4 address.
const ipv4Bits = 32

// ipv6Bits is the prefix length of one bare IPv6 address.
const ipv6Bits = 128

// ipv4MappedShift is the bit gap between an IPv4-mapped IPv6 prefix and the
// IPv4 prefix it names.
const ipv4MappedShift = 96

// xffHeader names the de facto standard forwarding header. When present it
// wins over Forwarded.
const xffHeader = "X-Forwarded-For"

// forwardedHeader names the forwarding header of RFC 7239.
const forwardedHeader = "Forwarded"

// Resolver answers the client address of a request against a fixed list of
// trusted proxies. The zero value trusts none: every request reads as its
// TCP peer.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver returns a Resolver over trusted. It copies the slice and masks
// each prefix, so later changes to the input change nothing.
func NewResolver(trusted []netip.Prefix) *Resolver {
	copied := make([]netip.Prefix, 0, len(trusted))
	for _, prefix := range trusted {
		copied = append(copied, prefix.Masked())
	}

	return &Resolver{trusted: copied}
}

// ParseList parses s as a comma-separated list of trusted proxies. Spaces
// around items are allowed and empty items are skipped. An item is a CIDR
// ("10.0.0.0/8", "2001:db8::/32") or a bare address, which reads as /32 or
// /128. An IPv4-mapped IPv6 item is unmapped first.
func ParseList(s string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0)

	for item := range strings.SplitSeq(s, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}

		prefix, parseErr := parseProxyItem(trimmed)
		if parseErr != nil {
			return nil, parseErr
		}

		out = append(out, prefix)
	}

	return out, nil
}

// parseProxyItem parses one trusted proxy: a CIDR or a bare address.
func parseProxyItem(item string) (netip.Prefix, error) {
	addr, addrErr := netip.ParseAddr(item)
	if addrErr == nil {
		return prefixOfAddr(addr), nil
	}

	prefix, prefixErr := netip.ParsePrefix(item)
	if prefixErr != nil {
		//nolint:err113 // The error must name the bad item, so it is dynamic by design.
		return netip.Prefix{}, fmt.Errorf("trusted proxy %q is not a CIDR or an IP address", item)
	}

	return unmapPrefix(prefix), nil
}

// prefixOfAddr returns the /32 or /128 prefix of addr after unmapping it and
// dropping its zone.
func prefixOfAddr(addr netip.Addr) netip.Prefix {
	unmapped := addr.Unmap().WithZone("")

	bits := ipv6Bits
	if unmapped.Is4() {
		bits = ipv4Bits
	}

	return netip.PrefixFrom(unmapped, bits)
}

// unmapPrefix masks prefix and maps an IPv4-mapped IPv6 prefix to its IPv4
// form, so "::ffff:10.0.0.0/104" reads as "10.0.0.0/8".
func unmapPrefix(prefix netip.Prefix) netip.Prefix {
	masked := prefix.Masked()
	addr := masked.Addr()

	if !addr.Is4In6() {
		return masked
	}

	bits := max(masked.Bits()-ipv4MappedShift, 0)
	bits = min(bits, ipv4Bits)

	return netip.PrefixFrom(addr.Unmap().WithZone(""), bits)
}

// Resolve returns the client address of req. A nil Resolver, or an empty
// list, reads as the peer only. The zero Addr means "unknown": the RemoteAddr
// holds no address.
func (r *Resolver) Resolve(req *http.Request) netip.Addr {
	if req == nil {
		return netip.Addr{}
	}

	peer := peerAddr(req.RemoteAddr)

	if !peer.IsValid() {
		return netip.Addr{}
	}

	if !r.contains(peer) {
		return peer
	}

	if xffPresent(req) {
		return resolveHops(xffValues(req), peer, r, parseHop)
	}

	if forwardedPresent(req) {
		return resolveHops(forwardedFors(req), peer, r, parseForwardedHop)
	}

	return peer
}

// contains reports whether addr is in the trusted list. A nil Resolver
// trusts none.
func (r *Resolver) contains(addr netip.Addr) bool {
	if r == nil {
		return false
	}

	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

// peerAddr parses the host of a RemoteAddr value: the host of "ip:port" or
// "[v6]:port", else the whole value. It unmaps the address and drops its
// zone. A value with no address reads as the zero Addr.
func peerAddr(remote string) netip.Addr {
	host := remote

	splitHost, _, splitErr := net.SplitHostPort(remote)
	if splitErr == nil {
		host = splitHost
	}

	addr, parseErr := netip.ParseAddr(host)
	if parseErr != nil {
		return netip.Addr{}
	}

	return addr.Unmap().WithZone("")
}

// xffPresent reports whether the request carries X-Forwarded-For lines.
func xffPresent(req *http.Request) bool {
	return len(req.Header.Values(xffHeader)) > 0
}

// forwardedPresent reports whether the request carries Forwarded lines.
func forwardedPresent(req *http.Request) bool {
	return len(req.Header.Values(forwardedHeader)) > 0
}

// xffValues joins all X-Forwarded-For lines in order and splits them into
// trimmed elements.
func xffValues(req *http.Request) []string {
	var out []string

	for _, line := range req.Header.Values(xffHeader) {
		for elem := range strings.SplitSeq(line, ",") {
			out = append(out, strings.TrimSpace(elem))
		}
	}

	return out
}

// parseHop parses one address of an X-Forwarded-For list: "ip", or "ip:port"
// ("[v6]:port" for IPv6). It unmaps the address and drops its zone.
func parseHop(s string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return netip.Addr{}, false
	}

	addr, addrErr := netip.ParseAddr(trimmed)
	if addrErr == nil {
		return addr.Unmap().WithZone(""), true
	}

	withPort, portErr := netip.ParseAddrPort(trimmed)
	if portErr != nil {
		return netip.Addr{}, false
	}

	return withPort.Addr().Unmap().WithZone(""), true
}

// forwardedFors returns the for value of each Forwarded element that holds
// one, in order. Elements without one count as absent.
func forwardedFors(req *http.Request) []string {
	var out []string

	for _, line := range req.Header.Values(forwardedHeader) {
		for elem := range strings.SplitSeq(line, ",") {
			value, ok := forwardedForOf(elem)
			if ok {
				out = append(out, value)
			}
		}
	}

	return out
}

// forwardedForOf returns the for value of one Forwarded element. The element
// holds semicolon-separated name=value pairs and the name match ignores case.
func forwardedForOf(elem string) (string, bool) {
	for pair := range strings.SplitSeq(elem, ";") {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}

		if !strings.EqualFold(strings.TrimSpace(name), "for") {
			continue
		}

		return strings.TrimSpace(value), true
	}

	return "", false
}

// parseForwardedHop parses one Forwarded for value. It takes a quoted
// string, an IPv6 "[addr]" value with an optional ":port" suffix, or a plain
// address with the same rules as X-Forwarded-For. "unknown" and obfuscated
// identifiers that start with "_" do not parse.
func parseForwardedHop(s string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(unquote(s))

	switch {
	case trimmed == "":
		return netip.Addr{}, false
	case strings.EqualFold(trimmed, "unknown"):
		return netip.Addr{}, false
	case strings.HasPrefix(trimmed, "_"):
		return netip.Addr{}, false
	case strings.HasPrefix(trimmed, "["):
		return parseBracketed(trimmed)
	default:
		return parseHop(trimmed)
	}
}

// unquote removes one pair of double quotes around s. RFC 7239 allows a
// quoted for value.
func unquote(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) >= 2 && strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}

	return trimmed
}

// parseBracketed parses "[addr]" with an optional ":port" suffix. It unmaps
// the address and drops its zone.
func parseBracketed(s string) (netip.Addr, bool) {
	end := strings.Index(s, "]")
	if end < 0 {
		return netip.Addr{}, false
	}

	rest := s[end+1:]
	if rest != "" && !strings.HasPrefix(rest, ":") {
		return netip.Addr{}, false
	}

	addr, parseErr := netip.ParseAddr(strings.TrimSpace(s[1:end]))
	if parseErr != nil {
		return netip.Addr{}, false
	}

	return addr.Unmap().WithZone(""), true
}

// resolveHops walks raw from the right and returns the first address outside
// the trusted list. An entry that does not parse, or an empty entry, ends
// the walk with the peer: the server never trusts a value it cannot read.
// When every entry is trusted the answer is the leftmost entry. More than
// maxForwardedHops entries read as the peer.
func resolveHops(
	raw []string, peer netip.Addr, r *Resolver, parse func(string) (netip.Addr, bool),
) netip.Addr {
	if len(raw) > maxForwardedHops {
		return peer
	}

	hops := make([]netip.Addr, 0, len(raw))

	for _, item := range raw {
		addr, ok := parse(item)
		if !ok {
			return peer
		}

		hops = append(hops, addr)
	}

	for _, hop := range slices.Backward(hops) {
		if !r.contains(hop) {
			return hop
		}
	}

	if len(hops) == 0 {
		return peer
	}

	return hops[0]
}

// addrKey holds the resolved client address in the request context.
type addrKey struct{}

// WithAddr stores a in ctx. The request log middleware stores the Resolve
// answer, so handlers read the same address without reading headers again.
func WithAddr(ctx context.Context, a netip.Addr) context.Context {
	return context.WithValue(ctx, addrKey{}, a)
}

// Of returns the client address that Resolve stored in the request context
// with WithAddr. When the context holds none, it returns the RemoteAddr peer
// only and never reads a header.
func Of(req *http.Request) netip.Addr {
	if req == nil {
		return netip.Addr{}
	}

	stored, ok := req.Context().Value(addrKey{}).(netip.Addr)
	if ok {
		return stored
	}

	return peerAddr(req.RemoteAddr)
}

// String returns Of(req) in text. When Of is the zero Addr, it returns the
// raw RemoteAddr with a port removed when it holds one, else unchanged.
func String(req *http.Request) string {
	if req == nil {
		return ""
	}

	addr := Of(req)
	if addr.IsValid() {
		return addr.String()
	}

	host, _, splitErr := net.SplitHostPort(req.RemoteAddr)
	if splitErr != nil {
		return req.RemoteAddr
	}

	return host
}
