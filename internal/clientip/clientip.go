// Package clientip answers the client address of a request.
//
// The server writes the address to the request log and to the source of each
// audit entry. By default the address is the TCP peer only. When the operator
// names trusted proxies, the server also reads the one forwarding header the
// trusted-proxy-header setting names: X-Forwarded-For or Forwarded.
//
// A client can write any value into these headers. The resolver walks the
// header from the right and takes the first address outside the trusted list.
// A value that a client prepends never wins over the value that a trusted
// proxy added. The resolver reads only the named header, never the other one.
// Headers of a peer outside the list never count.
//
// In every doubt the answer is the zero Addr (unknown), never the peer:
// allowlists deny an unknown address. A trusted peer whose request carries no
// line of the named header reads as unknown, not as the peer.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// maxForwardedHops caps the trusted entries of one header walk. A walk that
// passes this many trusted entries without finding an untrusted address reads
// as unknown: no client can make the walk grow without a bound.
const maxForwardedHops = 32

// ipv4Bits is the prefix length of one bare IPv4 address.
const ipv4Bits = 32

// ipv6Bits is the prefix length of one bare IPv6 address.
const ipv6Bits = 128

// ipv4MappedShift is the bit gap between an IPv4-mapped IPv6 prefix and the
// IPv4 prefix it names.
const ipv4MappedShift = 96

// maxPortValue is the largest port a forwarding header may name.
const maxPortValue = 65535

// maxPortDigits caps the digits of a port in a forwarding header. A longer
// value is out of range without parsing it.
const maxPortDigits = 5

// asciiZero and asciiNine bound the ASCII digits that a port in a forwarding
// header may hold.
const (
	asciiZero = '0'
	asciiNine = '9'
)

// xffHeader names the de facto standard forwarding header.
const xffHeader = "X-Forwarded-For"

// forwardedHeader names the forwarding header of RFC 7239.
const forwardedHeader = "Forwarded"

// Header names the one forwarding header the resolver reads.
type Header string

// The two forwarding headers the trusted-proxy-header setting takes.
const (
	// HeaderXForwardedFor is the de facto standard forwarding header, and
	// the default setting.
	HeaderXForwardedFor Header = "x-forwarded-for"
	// HeaderForwarded is the forwarding header of RFC 7239.
	HeaderForwarded Header = "forwarded"
)

// ParseHeader parses the trusted-proxy-header setting: empty means the
// default. The match ignores case.
func ParseHeader(s string) (Header, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return HeaderXForwardedFor, nil
	}

	lowered := Header(strings.ToLower(trimmed))

	switch lowered {
	case HeaderXForwardedFor, HeaderForwarded:
		return lowered, nil
	default:
		//nolint:err113 // The error must name the bad value, so it is dynamic by design.
		return "", fmt.Errorf("trusted proxy header %q is not x-forwarded-for or forwarded", s)
	}
}

// Resolver answers the client address of a request against a fixed list of
// trusted proxies. The zero value trusts none: every request reads as its
// TCP peer.
type Resolver struct {
	trusted []netip.Prefix
	header  Header
}

// NewResolver returns a Resolver over trusted that reads header. It copies
// the slice, so later changes to the input change nothing. An empty header
// reads as the default. It refuses a list that covers every address or holds
// host bits, and an unknown header, so the server never starts with them.
func NewResolver(trusted []netip.Prefix, header Header) (*Resolver, error) {
	if header == "" {
		header = HeaderXForwardedFor
	}

	if header != HeaderXForwardedFor && header != HeaderForwarded {
		//nolint:err113 // The error must name the bad value, so it is dynamic by design.
		return nil, fmt.Errorf("trusted proxy header %q is not x-forwarded-for or forwarded", string(header))
	}

	copied := make([]netip.Prefix, 0, len(trusted))

	for _, prefix := range trusted {
		checked, checkErr := checkPrefix(prefix.String(), prefix)
		if checkErr != nil {
			return nil, checkErr
		}

		copied = append(copied, checked)
	}

	return &Resolver{trusted: copied, header: header}, nil
}

// ParseList parses s as a comma-separated list of trusted proxies. Spaces
// around items are allowed and empty items are skipped. An item is a CIDR
// ("10.0.0.0/8", "2001:db8::/32") or a bare address, which reads as /32 or
// /128. An IPv4-mapped IPv6 item is unmapped first. It refuses a /0 prefix
// and a prefix with host bits set, so the server never starts with them.
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

	checked, checkErr := checkPrefix(item, prefix)
	if checkErr != nil {
		return netip.Prefix{}, checkErr
	}

	return checked, nil
}

// checkPrefix validates one trusted proxy and answers its masked, unmapped
// form. It refuses a prefix that covers every address and one with host bits
// set: both would let a client read as a trusted address.
func checkPrefix(item string, prefix netip.Prefix) (netip.Prefix, error) {
	if !prefix.IsValid() {
		//nolint:err113 // The error must name the bad item, so it is dynamic by design.
		return netip.Prefix{}, fmt.Errorf("trusted proxy %q is not a CIDR or an IP address", item)
	}

	unmapped := unmapPrefix(prefix)

	if unmapped.Bits() == 0 {
		//nolint:err113 // The error must name the bad item, so it is dynamic by design.
		return netip.Prefix{}, fmt.Errorf(
			"trusted proxy %q covers every address: list the proxies by network or by address",
			item,
		)
	}

	if prefix != prefix.Masked() {
		//nolint:err113 // The error must name the bad item and its network, so it is dynamic by design.
		return netip.Prefix{}, fmt.Errorf(
			"trusted proxy %q has host bits set: write the network (%s) or one address (/32)",
			item,
			unmapped.String(),
		)
	}

	return unmapped, nil
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
// form, so "::ffff:10.0.0.0/104" reads as "10.0.0.0/8". The unmap reads the
// original address, so "::ffff:0:0/80" reads as "0.0.0.0/0": masking first
// would drop the mapped form before the shift.
func unmapPrefix(prefix netip.Prefix) netip.Prefix {
	addr := prefix.Addr()

	if !addr.Is4In6() {
		return prefix.Masked()
	}

	bits := max(prefix.Bits()-ipv4MappedShift, 0)
	bits = min(bits, ipv4Bits)

	return netip.PrefixFrom(addr.Unmap().WithZone(""), bits).Masked()
}

// Resolve returns the client address of req. A nil Resolver, an empty list,
// or a peer outside the list reads as the peer only and never reads a
// header. The zero Addr means "unknown": the RemoteAddr holds no address, no
// line of the named header is present on a request from a trusted peer, or
// the header holds a value the server cannot read. A trusted peer with no
// line of the named header reads as unknown, never as the peer: a
// misconfigured proxy, or a path that skips the header, must never make
// every client read as an internal address.
func (r *Resolver) Resolve(req *http.Request) netip.Addr {
	if req == nil {
		return netip.Addr{}
	}

	peer := peerAddr(req.RemoteAddr)

	if !peer.IsValid() {
		return netip.Addr{}
	}

	if r == nil || !r.contains(peer) {
		return peer
	}

	lines := req.Header.Values(r.headerKey())
	if len(lines) == 0 {
		return netip.Addr{}
	}

	entries, ok := headerEntries(r.header, lines)
	if !ok {
		return netip.Addr{}
	}

	return r.resolveHops(entries)
}

// Header returns the header the resolver reads, or the default for a nil or
// zero Resolver.
func (r *Resolver) Header() Header {
	if r == nil || r.header == "" {
		return HeaderXForwardedFor
	}

	return r.header
}

// TrustedPeerWithoutHeader reports whether req came from a trusted peer and
// carries no line of the named header. The server warns on such requests: a
// proxy that sends no forwarding header would otherwise make every client
// read as an internal address.
func (r *Resolver) TrustedPeerWithoutHeader(req *http.Request) bool {
	if req == nil || r == nil {
		return false
	}

	peer := peerAddr(req.RemoteAddr)

	if !peer.IsValid() || !r.contains(peer) {
		return false
	}

	return len(req.Header.Values(r.headerKey())) == 0
}

// headerKey returns the wire name of the header the resolver reads.
func (r *Resolver) headerKey() string {
	if r.Header() == HeaderForwarded {
		return forwardedHeader
	}

	return xffHeader
}

// Peer returns the TCP peer of req without zone, or the zero Addr when the
// RemoteAddr holds no address. It never reads a header.
func Peer(req *http.Request) netip.Addr {
	if req == nil {
		return netip.Addr{}
	}

	return peerAddr(req.RemoteAddr)
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

// headerEntries joins the lines of the named header in order and splits them
// into one entry per address, so several header lines read as one list. It
// reports false when a Forwarded line ends inside quotes: the whole header
// is then unreadable. An X-Forwarded-For line has no quote handling and
// always reports true.
func headerEntries(header Header, lines []string) ([]string, bool) {
	if header == HeaderForwarded {
		return forwardedFors(lines)
	}

	return xffValues(lines), true
}

// xffValues joins X-Forwarded-For lines in order and splits them into
// trimmed elements.
func xffValues(lines []string) []string {
	out := make([]string, 0)

	for _, line := range lines {
		for elem := range strings.SplitSeq(line, ",") {
			out = append(out, strings.TrimSpace(elem))
		}
	}

	return out
}

// parseHop parses one address of an X-Forwarded-For list: "ip", "ip:port" or
// "[v6]:port". A port holds digits 0 to 65535. It unmaps the address and
// drops its zone.
func parseHop(s string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return netip.Addr{}, false
	}

	if strings.HasPrefix(trimmed, "[") {
		return parseBracketed(trimmed)
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

// forwardedFors returns one entry per Forwarded element, in order. The entry
// is the for value, or an empty string when the element holds no for pair: an
// empty entry never parses, so the walk ends with unknown instead of
// skipping it. It reports false when any line ends inside a quoted string or
// after a dangling backslash inside quotes: such a line may hide the comma
// that a trusted proxy appended, so the whole header is unreadable. Each
// line is checked on its own: a quote never spans two lines.
func forwardedFors(lines []string) ([]string, bool) {
	out := make([]string, 0)

	for _, line := range lines {
		if !quotesBalanced(line) {
			return out, false
		}

		for _, elem := range splitOutsideQuotes(line, ',') {
			value, ok := forwardedForOf(elem)
			if !ok {
				out = append(out, "")

				continue
			}

			out = append(out, value)
		}
	}

	return out, true
}

// quotesBalanced reports whether every double-quoted section of line closes
// and no backslash inside quotes dangles at its end. It uses the same quote
// and escape rules as splitOutsideQuotes: a backslash escapes the next byte
// only inside quotes, and outside quotes it is an ordinary byte.
func quotesBalanced(line string) bool {
	quoted := false
	escaped := false

	for i := range len(line) {
		c := line[i]

		switch {
		case escaped:
			escaped = false
		case quoted && c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		default:
		}
	}

	return !quoted && !escaped
}

// forwardedForOf returns the for value of one Forwarded element. The element
// holds semicolon-separated name=value pairs and the name match ignores case.
// An element with two for pairs has no usable for: it returns the empty
// entry, which never parses, so the walk ends with unknown.
func forwardedForOf(elem string) (string, bool) {
	foundFor := false
	first := ""

	for _, pair := range splitOutsideQuotes(elem, ';') {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}

		if !strings.EqualFold(strings.TrimSpace(name), "for") {
			continue
		}

		if foundFor {
			return "", true
		}

		first = strings.TrimSpace(value)
		foundFor = true
	}

	if !foundFor {
		return "", false
	}

	return first, true
}

// splitOutsideQuotes splits s on each sep outside double-quoted sections. A
// backslash escapes the next byte inside quotes.
func splitOutsideQuotes(s string, sep byte) []string {
	out := make([]string, 0)

	start := 0
	quoted := false
	escaped := false

	for i := range len(s) {
		c := s[i]

		switch {
		case escaped:
			escaped = false
		case quoted && c == '\\':
			escaped = true
		case c == '"':
			quoted = !quoted
		case c == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		default:
		}
	}

	return append(out, s[start:])
}

// parseForwardedHop parses one Forwarded for value: a bare IPv4 address with
// an optional :port, or a quoted "[v6]" host with an optional :port outside
// the quotes. IPv6 without brackets and quotes never parses. "unknown" and
// obfuscated identifiers that start with "_" never parse, and neither does a
// port outside 0 to 65535.
func parseForwardedHop(s string) (netip.Addr, bool) {
	parsed := splitForwardedValue(s)
	if !parsed.ok {
		return netip.Addr{}, false
	}

	trimmed := strings.TrimSpace(parsed.host)

	switch {
	case trimmed == "":
		return netip.Addr{}, false
	case strings.EqualFold(trimmed, "unknown"):
		return netip.Addr{}, false
	case strings.HasPrefix(trimmed, "_"):
		return netip.Addr{}, false
	case strings.HasPrefix(trimmed, "["):
		if !parsed.quoted {
			return netip.Addr{}, false
		}

		return parseForwardedBracketed(trimmed, parsed.port)
	default:
		return parseForwardedBare(trimmed, parsed.port)
	}
}

// forwardedValue is one split for value: the host part, the port outside the
// quotes, whether the host was quoted, and whether the value split.
type forwardedValue struct {
	host   string
	port   string
	quoted bool
	ok     bool
}

// splitForwardedValue splits a for value into its host part and its port part
// outside the quotes, and reports whether the host was quoted. A bare value
// carries no separate port. An unquoted bracketed host is refused here: IPv6
// must be quoted.
func splitForwardedValue(raw string) forwardedValue {
	s := strings.TrimSpace(raw)
	if s == "" {
		return forwardedValue{host: "", port: "", quoted: false, ok: false}
	}

	if !strings.HasPrefix(s, "\"") {
		if strings.HasPrefix(s, "[") {
			return forwardedValue{host: "", port: "", quoted: false, ok: false}
		}

		return forwardedValue{host: s, port: "", quoted: false, ok: true}
	}

	end := closingQuote(s)
	if end < 0 {
		return forwardedValue{host: "", port: "", quoted: false, ok: false}
	}

	host := unescapeQuoted(s[1:end])
	rest := strings.TrimSpace(s[end+1:])

	if rest == "" {
		return forwardedValue{host: host, port: "", quoted: true, ok: true}
	}

	port, found := strings.CutPrefix(rest, ":")
	if !found {
		return forwardedValue{host: "", port: "", quoted: false, ok: false}
	}

	return forwardedValue{host: host, port: strings.TrimSpace(port), quoted: true, ok: true}
}

// closingQuote answers the index of the closing unescaped double quote of a
// value that starts with one, or -1 when the value never closes.
func closingQuote(s string) int {
	escaped := false
	tail := s[1:]

	for i := range len(tail) {
		c := tail[i]

		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			return i + 1
		default:
		}
	}

	return -1
}

// unescapeQuoted resolves backslash escapes inside a quoted string.
func unescapeQuoted(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}

	var out strings.Builder

	escaped := false

	for i := range len(s) {
		c := s[i]

		switch {
		case escaped:
			out.WriteByte(c)

			escaped = false
		case c == '\\':
			escaped = true
		default:
			out.WriteByte(c)
		}
	}

	return out.String()
}

// parseForwardedBracketed parses a quoted "[v6]" host with an optional port.
// The caller refuses an unquoted host: IPv6 must be quoted.
func parseForwardedBracketed(host, port string) (netip.Addr, bool) {
	combined := host
	if port != "" {
		combined = host + ":" + port
	}

	return parseBracketed(combined)
}

// parseForwardedBare parses a bare for host, which must be IPv4, with an
// optional port: either inside the host ("1.2.3.4:5678") or outside the
// quotes of a quoted IPv4 value. A bare IPv6 value never parses: it must use
// brackets and quotes. The port holds digits 0 to 65535.
func parseForwardedBare(host, port string) (netip.Addr, bool) {
	name, inside, ok := splitBareHost(host)
	if !ok {
		return netip.Addr{}, false
	}

	if inside != "" {
		port = inside
	}

	if port != "" && !validPort(port) {
		return netip.Addr{}, false
	}

	addr, parseErr := netip.ParseAddr(name)
	if parseErr != nil || !addr.Is4() {
		return netip.Addr{}, false
	}

	return addr.Unmap().WithZone(""), true
}

// splitBareHost splits a bare for host into its address part and the port
// part inside it, or an empty port when the host holds none. A host with more
// than one colon never splits: only "address:port" parses. The two strings
// stay unnamed, as nonamedreturns requires; the first is the address, the
// second is the port.
//
//nolint:revive // Unnamed two-string result per nonamedreturns; order is documented.
func splitBareHost(host string) (string, string, bool) {
	trimmed := strings.TrimSpace(host)
	if !strings.Contains(trimmed, ":") {
		return trimmed, "", true
	}

	name, port, _ := strings.Cut(trimmed, ":")
	if strings.Contains(port, ":") {
		return "", "", false
	}

	return strings.TrimSpace(name), strings.TrimSpace(port), true
}

// parseBracketed parses "[addr]" with an optional ":port" suffix. The port
// holds digits 0 to 65535. It unmaps the address and drops its zone.
func parseBracketed(s string) (netip.Addr, bool) {
	end := strings.Index(s, "]")
	if end < 0 {
		return netip.Addr{}, false
	}

	rest := s[end+1:]

	if rest != "" {
		port, found := strings.CutPrefix(rest, ":")
		if !found || !validPort(port) {
			return netip.Addr{}, false
		}
	}

	addr, parseErr := netip.ParseAddr(strings.TrimSpace(s[1:end]))
	if parseErr != nil {
		return netip.Addr{}, false
	}

	return addr.Unmap().WithZone(""), true
}

// validPort reports whether s holds a port: ASCII digits only, 0 to 65535.
// The digit check runs first, so "+80", "-0", "0x50" and non-ASCII digits
// never reach the parse.
func validPort(s string) bool {
	if s == "" || len(s) > maxPortDigits {
		return false
	}

	for i := range len(s) {
		if s[i] < asciiZero || s[i] > asciiNine {
			return false
		}
	}

	num, err := strconv.Atoi(s)
	if err != nil {
		return false
	}

	return num >= 0 && num <= maxPortValue
}

// resolveHops walks entries from the right and returns the first address
// outside the trusted list. Entries left of it never count and are never
// parsed. An entry that does not parse ends the walk with the zero Addr, as
// does a walk that passes maxForwardedHops trusted entries without finding an
// untrusted one. When every entry is trusted and fewer than the cap, the
// answer is the leftmost entry: the original client sits in the trusted
// range.
func (r *Resolver) resolveHops(entries []string) netip.Addr {
	parse := parseHop
	if r.header == HeaderForwarded {
		parse = parseForwardedHop
	}

	trusted := 0
	leftmost := netip.Addr{}

	for i := range slices.Backward(entries) {
		addr, ok := parse(entries[i])
		if !ok {
			return netip.Addr{}
		}

		if !r.contains(addr) {
			return addr
		}

		leftmost = addr
		trusted++

		if trusted >= maxForwardedHops {
			return netip.Addr{}
		}
	}

	return leftmost
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

// String returns Of(req) in text, or "" when the address is unknown. It never
// returns the raw RemoteAddr: an unparseable address reads as unknown.
func String(req *http.Request) string {
	if req == nil {
		return ""
	}

	addr := Of(req)
	if !addr.IsValid() {
		return ""
	}

	return addr.String()
}
