package selfservice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

// CanonicalJSON returns the canonical form of the JSON value data, in the
// manner of RFC 8785, the JSON Canonicalization Scheme: no white space,
// the members of each object sorted by the UTF-16 code units of their
// names, each number in the shortest form that reads back as the same
// IEEE 754 double, and each string with only the escapes that the scheme
// gives. So 1, 1.0 and 1e0 give 1, and "é" and "é" give "é". It
// returns ErrInvalid for data that is not one JSON value, and for a
// number that is not a finite double.
func CanonicalJSON(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the JSON value", ErrInvalid)
	}

	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(x))
	case json.Number:
		s, err := canonicalNumber(x)
		if err != nil {
			return err
		}

		buf.WriteString(s)
	case string:
		writeCanonicalString(buf, x)
	case []any:
		buf.WriteByte('[')

		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}

			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}

		buf.WriteByte(']')
	case map[string]any:
		return writeCanonicalObject(buf, x)
	default:
		return fmt.Errorf("%w: a JSON value of the type %T", ErrInvalid, v)
	}

	return nil
}

func writeCanonicalObject(buf *bytes.Buffer, m map[string]any) error {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}

	slices.SortFunc(names, compareUTF16)
	buf.WriteByte('{')

	for i, k := range names {
		if i > 0 {
			buf.WriteByte(',')
		}

		writeCanonicalString(buf, k)
		buf.WriteByte(':')

		if err := writeCanonical(buf, m[k]); err != nil {
			return err
		}
	}

	buf.WriteByte('}')

	return nil
}

// compareUTF16 orders two strings by their UTF-16 code units, as RFC 8785
// sorts the names of an object.
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// canonicalNumber returns n as the ECMAScript Number.prototype.toString
// gives it: an integer with no exponent below 1e21, else the shortest
// digits, with an exponent for a magnitude of 1e21 or more, or below 1e-6.
func canonicalNumber(n json.Number) (string, error) {
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return "", fmt.Errorf("%w: the number %s is not a finite double", ErrInvalid, n)
	}

	if f == 0 {
		return "0", nil
	}

	abs := math.Abs(f)
	if abs >= 1e21 || abs < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		// Go writes at least two digits of exponent, ECMAScript the fewest:
		// 1e-07 becomes 1e-7.
		mant, exp, ok := strings.Cut(s, "e")
		if !ok || len(exp) < 2 {
			return s, nil
		}

		return mant + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0"), nil
	}

	return strconv.FormatFloat(f, 'f', -1, 64), nil
}

// writeCanonicalString writes s with the escapes of RFC 8785: \" and \\,
// the short forms \b, \t, \n, \f and \r, \u00xx in lowercase hex for each
// other control character, and every other character as it is.
func writeCanonicalString(buf *bytes.Buffer, s string) {
	const (
		hex = "0123456789abcdef"
		// space is the first character that is not a control character.
		space = 0x20
	)

	buf.WriteByte('"')

	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\t':
			buf.WriteString(`\t`)
		case '\n':
			buf.WriteString(`\n`)
		case '\f':
			buf.WriteString(`\f`)
		case '\r':
			buf.WriteString(`\r`)
		default:
			if r < space {
				buf.WriteString(`\u00`)
				buf.WriteByte(hex[r>>4])
				buf.WriteByte(hex[r&0xf])

				continue
			}

			buf.WriteRune(r)
		}
	}

	buf.WriteByte('"')
}
