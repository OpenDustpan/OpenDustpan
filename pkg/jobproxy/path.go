package jobproxy

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Fixed reasons of a path pattern that Parse refuses.
const (
	reasonPathEmpty        = "the path pattern is empty or does not start with /"
	reasonPathTooLong      = "the path pattern is longer than 2048 bytes"
	reasonPathNotUTF8      = "the path pattern is not valid UTF-8"
	reasonPathSegment      = "a segment of the path pattern is not canonical"
	reasonPathRestNotLast  = "** is not the last segment of the path pattern"
	reasonPathVariable     = "a template of the path pattern is not a known variable"
	reasonPathPartialMatch = "a segment of the path pattern mixes * with other characters"
)

// Byte values of the path checks.
const (
	// hexLetterBase is the value of the hex digit A.
	hexLetterBase = 10
	// firstPrintable is the first ASCII byte that is not a control character.
	firstPrintable = 0x20
	// asciiDelete is the ASCII control character DEL.
	asciiDelete = 0x7f
)

// segmentKind is the kind of one segment of a path pattern.
type segmentKind int

const (
	// segmentLiteral matches one segment with the same bytes.
	segmentLiteral segmentKind = iota
	// segmentOne is "*": it matches exactly one segment.
	segmentOne
	// segmentRest is "**": it matches one or more segments, and is last.
	segmentRest
	// segmentVariable is "{{ variable }}": Render replaces it with a literal.
	segmentVariable
)

// segment is one segment of a path pattern.
type segment struct {
	kind segmentKind
	// text is the literal of segmentLiteral and the variable name of
	// segmentVariable.
	text string
}

// CanonicalPath returns p and true when p is a canonical path, and false
// when it is not. It never changes p. A canonical path:
//
//   - starts with "/" and is at most MaxPathBytes long. "/" alone is the root,
//   - is valid UTF-8, and its percent-encoded bytes are valid UTF-8 too,
//   - has no empty segment, so no "//" and no "/" at the end,
//   - has no dot segment ("." or ".."), also not encoded,
//   - has no "?", "#", ";", NUL, control character or "\",
//   - encodes no "/" (%2F), no "\" (%5C), no NUL (%00), no "%" (%25) and no
//     control character,
//   - percent-encodes only a byte that needs it, with uppercase hex digits,
//   - holds no character that looks like "/", "\\" or "." (see lookAlikes).
//
// A case of the path is never changed: "/API" and "/api" are two paths.
func CanonicalPath(p string) (string, bool) {
	if !pathFrame(p) {
		return "", false
	}

	if p == "/" {
		return p, true
	}

	for s := range strings.SplitSeq(p[1:], "/") {
		if !canonicalSegment(s) {
			return "", false
		}
	}

	return p, true
}

// pathFrame checks the length, the first byte and the UTF-8 of a path or a
// path pattern.
func pathFrame(p string) bool {
	return p != "" && len(p) <= MaxPathBytes && p[0] == '/' && utf8.ValidString(p)
}

// canonicalSegment reports whether s is one canonical, non-empty path
// segment. It is the one check of a segment: CanonicalPath, the literals of
// a path pattern and each rendered value use it.
func canonicalSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}

	decoded := make([]byte, 0, len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]

		if c != '%' {
			if !segmentByte(c) {
				return false
			}

			decoded = append(decoded, c)

			continue
		}

		if i+2 >= len(s) {
			return false
		}

		b, ok := upperHexByte(s[i+1], s[i+2])
		if !ok || !encodable(b) {
			return false
		}

		decoded = append(decoded, b)
		i += 2
	}

	return utf8.Valid(decoded) && !hasLookAlike(decoded)
}

// lookAlikes are characters that look like "/", "\" or "." and that a
// server can fold to them, for example with Unicode NFKC normalization or a
// best-fit code page. After the check of the job proxy, such a server could
// read a segment border or a dot segment, so a canonical segment holds none
// of them, also when it is percent-encoded.
func lookAlikes() []rune {
	return []rune{
		'\uFF0F', // fullwidth solidus
		'\uFF3C', // fullwidth reverse solidus
		'\u2215', // division slash
		'\u2216', // set minus
		'\u2044', // fraction slash
		'\u29F8', // big solidus
		'\u29F9', // big reverse solidus
		'\uFE68', // small reverse solidus
		'\uFF0E', // fullwidth full stop
		'\uFE52', // small full stop
		'\u2024', // one dot leader
		'\u2025', // two dot leader
		'\u2026', // horizontal ellipsis
	}
}

// hasLookAlike reports whether the decoded bytes of a segment hold a
// character of lookAlikes.
func hasLookAlike(decoded []byte) bool {
	look := lookAlikes()

	for _, r := range string(decoded) {
		if slices.Contains(look, r) {
			return true
		}
	}

	return false
}

// segmentByte reports whether c can stand as it is in a canonical segment:
// an unreserved character, a sub-delimiter other than ";", ":" or "@"
// (RFC 3986 pchar). Every other byte, including each byte of a non-ASCII
// character, must be percent-encoded.
func segmentByte(c byte) bool {
	if unreserved(c) {
		return true
	}

	return strings.IndexByte("!$&'()*+,=:@", c) >= 0
}

// unreserved reports whether c is an unreserved character of RFC 3986.
func unreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '-' || c == '.' || c == '_' || c == '~'
	}
}

// encodable reports whether a canonical segment can hold b percent-encoded.
// An unreserved byte needs no encoding, so its encoded form is a second
// spelling. "/" and "\" encoded hide a segment border, "%" encoded hides a
// second decoding, and NUL and control characters end strings in other
// programs.
func encodable(b byte) bool {
	switch {
	case unreserved(b):
		return false
	case b < firstPrintable, b == asciiDelete:
		return false
	default:
		return b != '/' && b != '\\' && b != '%'
	}
}

// upperHexByte decodes two uppercase hex digits.
func upperHexByte(hi, lo byte) (byte, bool) {
	h, okHi := upperHexDigit(hi)
	l, okLo := upperHexDigit(lo)

	return h<<4 | l, okHi && okLo
}

// upperHexDigit decodes one uppercase hex digit. A lowercase digit is a
// second spelling, so it is refused.
func upperHexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + hexLetterBase, true
	default:
		return 0, false
	}
}

// parsePattern parses a path pattern. It returns a fixed reason when the
// pattern is refused. The reason never holds the pattern.
func parsePattern(p string) ([]segment, string) {
	switch {
	case p == "" || p[0] != '/':
		return nil, reasonPathEmpty
	case len(p) > MaxPathBytes:
		return nil, reasonPathTooLong
	case !utf8.ValidString(p):
		return nil, reasonPathNotUTF8
	case p == "/":
		return []segment{}, ""
	}

	parts := strings.Split(p[1:], "/")
	segs := make([]segment, 0, len(parts))

	for i, part := range parts {
		seg, reason := parseSegment(part)
		if reason != "" {
			return nil, reason
		}

		if seg.kind == segmentRest && i != len(parts)-1 {
			return nil, reasonPathRestNotLast
		}

		segs = append(segs, seg)
	}

	return segs, ""
}

// parseSegment parses one segment of a path pattern.
func parseSegment(part string) (segment, string) {
	switch {
	case part == "*":
		return segment{kind: segmentOne, text: ""}, ""
	case part == "**":
		return segment{kind: segmentRest, text: ""}, ""
	case strings.HasPrefix(part, "{{") && strings.HasSuffix(part, "}}") && len(part) >= len("{{}}"):
		name := strings.Trim(part[2:len(part)-2], " ")
		if !validVariable(name) {
			return segment{kind: segmentLiteral, text: ""}, reasonPathVariable
		}

		return segment{kind: segmentVariable, text: name}, ""
	case strings.Contains(part, "*"):
		return segment{kind: segmentLiteral, text: ""}, reasonPathPartialMatch
	case !canonicalSegment(part):
		return segment{kind: segmentLiteral, text: ""}, reasonPathSegment
	default:
		return segment{kind: segmentLiteral, text: part}, ""
	}
}
