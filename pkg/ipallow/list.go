// Package ipallow models per-namespace IP allowlists (ADR 55, decision 8).
//
// An allowlist is a set of CIDRs of one namespace. The feature is off by
// default for an installation, and each namespace opts in with its own list.
// A namespace without a list holds the zero List: no rule. This step adds no
// HTTP, no setting and no enforcement; later steps add them.
package ipallow

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// MaxEntries caps the distinct prefixes of one list.
const MaxEntries = 64

// ipv4Bits is the prefix length of one bare IPv4 address.
const ipv4Bits = 32

// ipv6Bits is the prefix length of one bare IPv6 address.
const ipv6Bits = 128

// ipv4MappedShift is the bit gap between an IPv4-mapped IPv6 prefix and the
// IPv4 prefix it names.
const ipv4MappedShift = 96

// listSeparator joins the entries of the stored column value.
const listSeparator = ","

// ErrInvalidList reports an address list that does not parse.
var ErrInvalidList = errors.New("ipallow: invalid address list")

// List is a set of CIDRs. The zero List holds no entries: no rule.
type List struct {
	prefixes []netip.Prefix
}

// ParseList parses items as a list of CIDRs ("10.0.0.0/8", "2001:db8::/32")
// or bare addresses, which read as /32 or /128. An IPv4-mapped IPv6 item is
// unmapped first. Each prefix is masked. Duplicates are dropped and order is
// kept. An empty item list gives the zero List and no error. More than
// MaxEntries distinct prefixes, or an item that does not parse, gives an
// error that wraps ErrInvalidList and names the item.
func ParseList(items []string) (List, error) {
	if len(items) == 0 {
		return List{prefixes: nil}, nil
	}

	prefixes := make([]netip.Prefix, 0, len(items))

	for _, item := range items {
		prefix, err := parseItem(item)
		if err != nil {
			return List{}, err
		}

		if !slices.Contains(prefixes, prefix) {
			prefixes = append(prefixes, prefix)
		}
	}

	if len(prefixes) > MaxEntries {
		return List{}, fmt.Errorf("%w: more than %d addresses", ErrInvalidList, MaxEntries)
	}

	return List{prefixes: prefixes}, nil
}

// parseItem parses one CIDR or bare address.
func parseItem(item string) (netip.Prefix, error) {
	if addr, err := netip.ParseAddr(item); err == nil {
		return prefixOfAddr(addr), nil
	}

	prefix, err := netip.ParsePrefix(item)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: %q is not a CIDR or an IP address", ErrInvalidList, item)
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

// Contains reports whether addr is in the list. addr is unmapped and its zone
// is dropped first. The zero Addr is never contained.
func (l List) Contains(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}

	unmapped := addr.Unmap().WithZone("")

	for _, prefix := range l.prefixes {
		if prefix.Contains(unmapped) {
			return true
		}
	}

	return false
}

// IsEmpty reports whether the list holds no entries.
func (l List) IsEmpty() bool {
	return len(l.prefixes) == 0
}

// Strings returns the canonical text of each prefix, as a copy.
func (l List) Strings() []string {
	out := make([]string, 0, len(l.prefixes))
	for _, prefix := range l.prefixes {
		out = append(out, prefix.String())
	}

	return out
}

// Encode returns the entries joined with ",", the column value. It returns
// "" for the zero List.
func (l List) Encode() string {
	return strings.Join(l.Strings(), listSeparator)
}

// DecodeList is the inverse of Encode. "" gives the zero List. A bad value
// wraps ErrInvalidList.
func DecodeList(s string) (List, error) {
	if s == "" {
		return List{prefixes: nil}, nil
	}

	return ParseList(strings.Split(s, listSeparator))
}
