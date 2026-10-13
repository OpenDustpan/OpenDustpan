package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// scanBufferBytes sizes the read buffer of the meta reader. The reader never
// holds a whole state: it passes one byte at a time through this buffer.
const scanBufferBytes = 64 << 10

// maxScanDepth bounds how deeply a state document may nest. It is the depth limit
// of encoding/json, so no state that OpenTofu accepts is refused for depth. A
// deeper document is refused rather than walked.
const maxScanDepth = 10000

// minCaptureBytes is the first size of a kept value's buffer.
const minCaptureBytes = 4 << 10

// controlByteLimit is the first byte that JSON allows unescaped in a string.
const controlByteLimit = 0x20

// errCaptureFull reports that a kept value grew past its limit. The caller maps
// it to the error of the value it keeps.
var errCaptureFull = errors.New("kept value is over its limit")

// jsonScanner walks one JSON document from a byte stream. It checks the JSON
// grammar of every value it passes, and it never buffers a whole value: a string
// or a number is consumed byte by byte. The bytes of a value the domain keeps go
// into keep, which refuses to grow past its limit. A skipped value keeps nothing.
type jsonScanner struct {
	r     *bufio.Reader
	keep  *capture
	depth int
}

// capture holds the bytes of one kept value, up to limit.
type capture struct {
	buf   []byte
	limit int
}

// add appends b, or fails with errCaptureFull when the value is over its limit.
// The buffer doubles as it grows: append alone grows large slices by about a
// quarter, and the copies of those growths would cost several times the limit.
func (c *capture) add(b byte) error {
	if len(c.buf) >= c.limit {
		return errCaptureFull
	}

	if len(c.buf) == cap(c.buf) {
		grown := make([]byte, len(c.buf), min(max(cap(c.buf)+cap(c.buf), minCaptureBytes), c.limit))
		copy(grown, c.buf)
		c.buf = grown
	}

	c.buf = append(c.buf, b)

	return nil
}

// end checks that only whitespace follows the state object up to the end of the
// document. Trailing data would make OpenTofu refuse the state on every read.
func (s *jsonScanner) end() error {
	for {
		b, err := s.r.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return readFailure(err)
		}

		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return syntaxError("data after the state object")
		}
	}
}

// newJSONScanner returns a scanner over r.
func newJSONScanner(r io.Reader) *jsonScanner {
	return &jsonScanner{r: bufio.NewReaderSize(r, scanBufferBytes), keep: nil, depth: 0}
}

// read consumes one byte and keeps it when a value is being kept.
func (s *jsonScanner) read() (byte, error) {
	b, err := s.r.ReadByte()
	if err != nil {
		return 0, readFailure(err)
	}

	if s.keep != nil {
		if addErr := s.keep.add(b); addErr != nil {
			return 0, addErr
		}
	}

	return b, nil
}

// peek returns the next byte without consuming it.
func (s *jsonScanner) peek() (byte, error) {
	b, err := s.r.Peek(1)
	if err != nil {
		return 0, readFailure(err)
	}

	return b[0], nil
}

// expect consumes one byte that must be want.
func (s *jsonScanner) expect(want byte) error {
	b, err := s.read()
	if err != nil {
		return err
	}

	if b != want {
		return syntaxError(fmt.Sprintf("want %q, got %q", want, b))
	}

	return nil
}

// skipSpace consumes the insignificant whitespace of JSON.
func (s *jsonScanner) skipSpace() error {
	for {
		b, err := s.peek()
		if err != nil {
			return err
		}

		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return nil
		}

		if _, err := s.read(); err != nil {
			return err
		}
	}
}

// value consumes one JSON value of any kind and checks its grammar.
func (s *jsonScanner) value() error {
	b, err := s.peek()
	if err != nil {
		return err
	}

	switch {
	case b == '{':
		return s.object()
	case b == '[':
		return s.array()
	case b == '"':
		return s.str()
	case b == '-' || (b >= '0' && b <= '9'):
		return s.number()
	case b == 't':
		return s.literal("true")
	case b == 'f':
		return s.literal("false")
	case b == 'n':
		return s.literal("null")
	default:
		return syntaxError(fmt.Sprintf("unexpected byte %q", b))
	}
}

// enter counts one level of nesting, and refuses a document nested too deep.
func (s *jsonScanner) enter() error {
	s.depth++

	if s.depth > maxScanDepth {
		return syntaxError("state nests too deep")
	}

	return nil
}

// object consumes one JSON object.
func (s *jsonScanner) object() error {
	if err := s.enter(); err != nil {
		return err
	}

	defer func() { s.depth-- }()

	if err := s.expect('{'); err != nil {
		return err
	}

	if err := s.skipSpace(); err != nil {
		return err
	}

	if b, err := s.peek(); err != nil {
		return err
	} else if b == '}' {
		_, err := s.read()

		return err
	}

	for {
		if err := s.str(); err != nil {
			return err
		}

		if err := s.member(); err != nil {
			return err
		}

		done, err := s.separator('}')
		if err != nil || done {
			return err
		}
	}
}

// member consumes the colon and the value that follow a member name.
func (s *jsonScanner) member() error {
	if err := s.skipSpace(); err != nil {
		return err
	}

	if err := s.expect(':'); err != nil {
		return err
	}

	if err := s.skipSpace(); err != nil {
		return err
	}

	return s.value()
}

// separator consumes the comma or the closing byte that follows an entry, and
// reports whether the container closed.
func (s *jsonScanner) separator(closing byte) (bool, error) {
	if err := s.skipSpace(); err != nil {
		return false, err
	}

	b, err := s.read()
	if err != nil {
		return false, err
	}

	switch b {
	case ',':
		return false, s.skipSpace()
	case closing:
		return true, nil
	default:
		return false, syntaxError(fmt.Sprintf("unexpected byte %q after an entry", b))
	}
}

// array consumes one JSON array.
func (s *jsonScanner) array() error {
	if err := s.enter(); err != nil {
		return err
	}

	defer func() { s.depth-- }()

	if err := s.expect('['); err != nil {
		return err
	}

	if err := s.skipSpace(); err != nil {
		return err
	}

	if b, err := s.peek(); err != nil {
		return err
	} else if b == ']' {
		_, err := s.read()

		return err
	}

	for {
		if err := s.value(); err != nil {
			return err
		}

		done, err := s.separator(']')
		if err != nil || done {
			return err
		}
	}
}

// str consumes one JSON string, and checks its escapes and control bytes.
func (s *jsonScanner) str() error {
	if err := s.expect('"'); err != nil {
		return err
	}

	for {
		b, err := s.read()
		if err != nil {
			return err
		}

		switch {
		case b == '"':
			return nil
		case b == '\\':
			if err := s.escape(); err != nil {
				return err
			}
		case b < controlByteLimit:
			return syntaxError("control byte in a string")
		default:
		}
	}
}

// escape consumes the byte or the four hex digits that follow a backslash.
func (s *jsonScanner) escape() error {
	b, err := s.read()
	if err != nil {
		return err
	}

	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return nil
	case 'u':
		for range 4 {
			h, hErr := s.read()
			if hErr != nil {
				return hErr
			}

			if !isHex(h) {
				return syntaxError("bad unicode escape")
			}
		}

		return nil
	default:
		return syntaxError(fmt.Sprintf("bad escape %q", b))
	}
}

// number consumes one JSON number, with the grammar of RFC 8259.
func (s *jsonScanner) number() error {
	if err := s.integer(); err != nil {
		return err
	}

	if err := s.fraction(); err != nil {
		return err
	}

	return s.exponent()
}

// integer consumes the sign and the integer part of a number.
func (s *jsonScanner) integer() error {
	b, err := s.peek()
	if err != nil {
		return err
	}

	if b == '-' {
		if _, readErr := s.read(); readErr != nil {
			return readErr
		}
	}

	first, err := s.read()
	if err != nil {
		return err
	}

	switch {
	case first == '0':
		return nil
	case first >= '1' && first <= '9':
		return s.digits(0)
	default:
		return syntaxError("bad number")
	}
}

// fraction consumes an optional fraction of a number.
func (s *jsonScanner) fraction() error {
	b, err := s.peek()
	if err != nil {
		return err
	}

	if b != '.' {
		return nil
	}

	if _, readErr := s.read(); readErr != nil {
		return readErr
	}

	return s.digits(1)
}

// exponent consumes an optional exponent of a number.
func (s *jsonScanner) exponent() error {
	b, err := s.peek()
	if err != nil {
		return err
	}

	if b != 'e' && b != 'E' {
		return nil
	}

	if _, readErr := s.read(); readErr != nil {
		return readErr
	}

	b, err = s.peek()
	if err != nil {
		return err
	}

	if b == '+' || b == '-' {
		if _, signErr := s.read(); signErr != nil {
			return signErr
		}
	}

	return s.digits(1)
}

// digits consumes the decimal digits that follow, and checks that there are at least `least` of them.
func (s *jsonScanner) digits(least int) error {
	for i := 0; ; i++ {
		b, err := s.peek()
		if err != nil {
			return err
		}

		if b < '0' || b > '9' {
			if i < least {
				return syntaxError("missing digits in a number")
			}

			return nil
		}

		if _, err := s.read(); err != nil {
			return err
		}
	}
}

// literal consumes the word of true, false or null.
func (s *jsonScanner) literal(word string) error {
	for i := range len(word) {
		b, err := s.read()
		if err != nil {
			return err
		}

		if b != word[i] {
			return syntaxError("bad literal")
		}
	}

	return nil
}

// isHex reports whether b is a hexadecimal digit.
func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// syntaxError names a grammar error of the state document.
func syntaxError(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, detail)
}

// readFailure names a read that ended or failed inside the document.
func readFailure(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: state ends inside a value: %w", ErrInvalidState, io.ErrUnexpectedEOF)
	}

	return fmt.Errorf("%w: read state: %w", ErrInvalidState, err)
}
