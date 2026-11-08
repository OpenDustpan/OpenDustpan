package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
)

// ErrAuthInResponse is the error of a response stream that held a form of
// the credential. The stream stops at once.
var ErrAuthInResponse = errors.New("the response held a form of the credential")

// Bounds of the scan.
const (
	// scanChunk is the size of one read of the response.
	scanChunk = 32 << 10
	// hexCaseGap is the distance between the two cases of a hex letter.
	hexCaseGap = 'a' - 'A'
	// escapeDigits is the number of hex digits after a percent sign.
	escapeDigits = 2
	// minJSONForm is the shortest JSON string: the two quotes.
	minJSONForm = 2
)

// formsOf returns the forms of the secret that a response could hold: the
// plain value, base64 (standard and URL, with and without padding), the JSON
// escape form (with and without the HTML escape) and the percent form (query
// and path style, both cases of the hex digits). It returns no form for an
// empty secret.
func formsOf(secret string) [][]byte {
	if secret == "" {
		return nil
	}

	raw := []byte(secret)
	set := [][]byte{raw}

	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		set = append(set, []byte(enc.EncodeToString(raw)))
	}

	for _, escapeHTML := range []bool{false, true} {
		if j, ok := jsonEscape(secret, escapeHTML); ok {
			set = append(set, j)
		}
	}

	for _, p := range []string{url.QueryEscape(secret), url.PathEscape(secret)} {
		set = append(set, []byte(p), lowerHex(p))
	}

	out := make([][]byte, 0, len(set))

	for _, f := range set {
		if len(f) > 0 && !slices.ContainsFunc(out, func(o []byte) bool { return bytes.Equal(o, f) }) {
			out = append(out, f)
		}
	}

	return out
}

// jsonEscape returns the JSON string form of s without the quotes.
func jsonEscape(s string, escapeHTML bool) ([]byte, bool) {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(escapeHTML)

	if err := enc.Encode(s); err != nil {
		return nil, false
	}

	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(out) < minJSONForm {
		return nil, false
	}

	return append([]byte(nil), out[1:len(out)-1]...), true
}

// lowerHex returns p with the hex digits of each percent escape in lower case.
func lowerHex(p string) []byte {
	b := []byte(p)

	for i := 0; i+escapeDigits < len(b); i++ {
		if b[i] == '%' {
			for j := i + 1; j <= i+escapeDigits; j++ {
				if b[j] >= 'A' && b[j] <= 'F' {
					b[j] += hexCaseGap
				}
			}
		}
	}

	return b
}

// scanner finds the forms of the secrets in a stream, across the borders of
// the chunks.
type scanner struct {
	forms  [][]byte
	window int
}

// newScanner builds a scanner for the secrets. The window is as long as the
// longest form.
func newScanner(secrets []string) *scanner {
	s := &scanner{forms: nil, window: 0}

	for _, secret := range secrets {
		s.forms = append(s.forms, formsOf(secret)...)
	}

	for _, f := range s.forms {
		s.window = max(s.window, len(f))
	}

	return s
}

// holds reports whether b holds a form.
func (s *scanner) holds(b []byte) bool {
	for _, f := range s.forms {
		if bytes.Contains(b, f) {
			return true
		}
	}

	return false
}

// reader returns a reader that passes the bytes of r and stops with
// ErrAuthInResponse when the stream holds a form. It holds back the
// last window-1 bytes until more bytes or the end of r arrive, so that no
// part of a form reaches the caller before the whole form is known.
func (s *scanner) reader(r io.Reader) io.Reader {
	return &scanReader{s: s, r: r, held: nil, out: nil, err: nil}
}

type scanReader struct {
	s    *scanner
	r    io.Reader
	held []byte
	out  []byte
	err  error
}

// Read implements io.Reader.
func (sr *scanReader) Read(p []byte) (int, error) {
	for len(sr.out) == 0 {
		if sr.err != nil {
			return 0, sr.err
		}

		buf := make([]byte, scanChunk)

		n, err := sr.r.Read(buf)
		window := slices.Concat(sr.held, buf[:n])

		if sr.s.holds(window) {
			sr.err = ErrAuthInResponse
			sr.held, sr.out = nil, nil

			return 0, sr.err
		}

		keep := min(max(sr.s.window-1, 0), len(window))
		if err != nil {
			// The end: nothing more can complete a form, so release all.
			keep = 0
			sr.err = err
		}

		release := len(window) - keep
		sr.out = append([]byte(nil), window[:release]...)
		sr.held = append([]byte(nil), window[release:]...)
	}

	n := copy(p, sr.out)
	sr.out = sr.out[n:]

	return n, nil
}
