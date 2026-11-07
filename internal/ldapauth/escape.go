package ldapauth

import (
	"errors"
	"fmt"
	"strings"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

// This file holds the encoding of the values that the method puts in an
// LDAP filter or an LDAP DN. A value from outside the process never enters
// a filter or a DN without its escape.

// hexDigits are the digits of a hex escape.
const hexDigits = "0123456789abcdef"

// EscapeFilter returns s as an assertion value of an LDAP filter (RFC 4515
// section 3). It escapes each byte that is not printable ASCII, and the
// bytes "*", "(", ")" and "\", as a backslash and two hex digits. So no
// value of s can add a wildcard, a parenthesis or another filter item.
func EscapeFilter(s string) string {
	var b strings.Builder

	b.Grow(len(s))

	for i := range len(s) {
		c := s[i]
		if c < 0x20 || c >= 0x7f || c == '*' || c == '(' || c == ')' || c == '\\' {
			b.WriteByte('\\')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])

			continue
		}

		b.WriteByte(c)
	}

	return b.String()
}

// EscapeDNValue returns s as an attribute value of an LDAP DN (RFC 4514
// section 2.4). It escapes the characters `"`, "+", ",", ";", "<", ">",
// "=" and "\" with a backslash, a space or "#" at the start and a space at
// the end with a backslash, and each byte that is not printable ASCII as a
// backslash and two hex digits. So no value of s can add an RDN or an
// attribute to the DN.
func EscapeDNValue(s string) string {
	var b strings.Builder

	b.Grow(len(s))

	for i := range len(s) {
		c := s[i]

		switch {
		case c < 0x20 || c >= 0x7f:
			b.WriteByte('\\')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		case strings.IndexByte(`"+,;<>=\`, c) >= 0,
			i == 0 && (c == ' ' || c == '#'),
			i == len(s)-1 && c == ' ':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

// errDN reports a DN that does not parse, that is empty, or that has an
// attribute type of the wrong form.
var errDN = errors.New("the DN is not valid")

// canonicalDN parses dn and builds it again from its parts: each attribute
// type checked against an allowlist, each value through EscapeDNValue. A DN
// that the method writes, in a bind or in a filter, is always this form,
// so a value of the directory cannot change the structure of the DN.
func canonicalDN(dn string) (string, *ldap.DN, error) {
	parsed, err := ldap.ParseDN(dn)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", errDN, err)
	}

	if len(parsed.RDNs) == 0 {
		return "", nil, fmt.Errorf("%w: the DN is empty", errDN)
	}

	rdns := make([]string, 0, len(parsed.RDNs))

	for _, rdn := range parsed.RDNs {
		if rdn == nil || len(rdn.Attributes) == 0 {
			return "", nil, fmt.Errorf("%w: an RDN is empty", errDN)
		}

		parts := make([]string, 0, len(rdn.Attributes))

		for _, a := range rdn.Attributes {
			if a == nil || !validAttributeType(a.Type) {
				return "", nil, fmt.Errorf("%w: an attribute type has the wrong form", errDN)
			}

			parts = append(parts, a.Type+"="+EscapeDNValue(a.Value))
		}

		rdns = append(rdns, strings.Join(parts, "+"))
	}

	return strings.Join(rdns, ","), parsed, nil
}

// maxAttributeType is the longest attribute type that the method takes,
// in bytes.
const maxAttributeType = 128

// validAttributeType reports whether s is an attribute type of RFC 4512
// section 1.4: a descriptor (a letter, then letters, digits and hyphens) or
// a numeric OID, with at most maxAttributeType bytes.
func validAttributeType(s string) bool {
	if s == "" || len(s) > maxAttributeType {
		return false
	}

	if isLetter(s[0]) {
		for i := range len(s) {
			if !isLetter(s[i]) && !isDigit(s[i]) && s[i] != '-' {
				return false
			}
		}

		return true
	}

	for part := range strings.SplitSeq(s, ".") {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}

	return true
}

// isLetter reports whether c is an ASCII letter.
func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// filterNodes returns the count of the items of the compiled filter f. Two
// filters of one template have the same count when each value went through
// EscapeFilter. A value that adds an item changes the count.
func filterNodes(f string) (int, error) {
	packet, err := ldap.CompileFilter(f)
	if err != nil {
		return 0, fmt.Errorf("compile the filter: %w", err)
	}

	return countPackets(packet), nil
}

// countPackets returns the count of p and of each packet below it.
func countPackets(p *ber.Packet) int {
	if p == nil {
		return 0
	}

	n := 1
	for _, c := range p.Children {
		n += countPackets(c)
	}

	return n
}
