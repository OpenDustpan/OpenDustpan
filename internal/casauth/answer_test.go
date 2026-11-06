package casauth_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/casauth"
)

// open and closing enclose a success answer.
const (
	open    = `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas"><cas:authenticationSuccess>`
	closing = `</cas:authenticationSuccess></cas:serviceResponse>`
)

// TestParseResponse checks the parser alone, with valid and hostile
// answers.
func TestParseResponse(t *testing.T) {
	t.Parallel()

	// deep returns a failure answer whose elements nest n deep: the root,
	// the failure and n-2 elements in the failure.
	deep := func(n int) string {
		return `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">` +
			`<cas:authenticationFailure code="INVALID_TICKET">` + strings.Repeat("<cas:a>", n-2) +
			strings.Repeat("</cas:a>", n-2) + `</cas:authenticationFailure></cas:serviceResponse>`
	}

	valid := []struct {
		name string
		body string
		user string
		code string
	}{
		{
			name: "a success", user: "alice", code: "",
			body: `<?xml version="1.0" encoding="UTF-8"?>` + open + "<cas:user> alice </cas:user>" +
				"<cas:attributes><cas:memberOf>ops</cas:memberOf></cas:attributes>" + closing,
		},
		{
			name: "a failure", user: "", code: "INVALID_TICKET",
			body: `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">` +
				`<cas:authenticationFailure code="INVALID_TICKET">no</cas:authenticationFailure></cas:serviceResponse>`,
		},
		{
			name: "a comment and space after the root", user: "alice", code: "",
			body: open + "<cas:user>alice</cas:user>" + closing + "\n<!-- end -->\n",
		},
		{name: "the depth of the limit", user: "", code: "INVALID_TICKET", body: deep(casauth.MaxAnswerDepth)},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := casauth.ParseResponse([]byte(tc.body))
			if err != nil || got.User != tc.user || got.FailureCode != tc.code || got.Success != (tc.code == "") {
				t.Fatalf("ParseResponse() = %+v, %v, want the user %q and the code %q", got, err, tc.user, tc.code)
			}
		})
	}

	hostile := []struct {
		name string
		body string
	}{
		{name: "one more than the depth of the limit", body: deep(casauth.MaxAnswerDepth + 1)},
		{name: "a DTD", body: `<!DOCTYPE x [<!ENTITY a "b">]>` + open + "<cas:user>&a;</cas:user>" + closing},
		{name: "an external entity", body: `<!DOCTYPE x [<!ENTITY a SYSTEM "file:///etc/passwd">]>` + open +
			"<cas:user>&a;</cas:user>" + closing},
		{name: "an undefined entity", body: open + "<cas:user>&a;</cas:user>" + closing},
		{name: "a processing instruction", body: `<?xml-stylesheet href="x"?>` + open + "<cas:user>a</cas:user>" +
			closing},
		{name: "another encoding", body: `<?xml version="1.0" encoding="ISO-8859-1"?>` + open +
			"<cas:user>a</cas:user>" + closing},
		{name: "not UTF-8", body: open + "<cas:user>\xff</cas:user>" + closing},
		{name: "another root", body: `<x:serviceResponse xmlns:x="urn:other"><x:authenticationSuccess>` +
			`<x:user>a</x:user></x:authenticationSuccess></x:serviceResponse>`},
		{name: "a success and a failure", body: `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">` +
			`<cas:authenticationSuccess><cas:user>a</cas:user></cas:authenticationSuccess>` +
			`<cas:authenticationFailure code="INVALID_TICKET"/></cas:serviceResponse>`},
		{name: "no user", body: open + closing},
		{name: "an element in the user", body: open + "<cas:user>al<cas:x>mallory</cas:x>ice</cas:user>" + closing},
		{name: "an element in the user of another namespace", body: open +
			`<cas:user>al<x:x xmlns:x="urn:other"/>ice</cas:user>` + closing},
		{name: "a second root", body: open + "<cas:user>alice</cas:user>" + closing + open +
			"<cas:user>bob</cas:user>" + closing},
		{name: "an element after the root", body: open + "<cas:user>alice</cas:user>" + closing + "<x/>"},
		{name: "a stray end tag and a second root", body: open + "<cas:user>alice</cas:user>" + closing + "</x>" +
			open + "<cas:user>bob</cas:user>" + closing},
		{name: "a stray end tag and an element after the root", body: open + "<cas:user>alice</cas:user>" +
			closing + "</x><x/>"},
		{name: "a stray end tag after the root", body: open + "<cas:user>alice</cas:user>" + closing + "</x>"},
		{name: "a stray end tag before the root", body: "</x>" + open + "<cas:user>alice</cas:user>" + closing},
		{name: "text after the root", body: open + "<cas:user>alice</cas:user>" + closing + "junk text"},
		{name: "text before the root", body: "junk" + open + "<cas:user>alice</cas:user>" + closing},
		{name: "an attribute in another namespace", body: open + `<cas:user>a</cas:user><cas:attributes>` +
			`<x:memberOf xmlns:x="urn:other">ops</x:memberOf></cas:attributes>` + closing},
		{name: "an attribute with an element", body: open + `<cas:user>a</cas:user><cas:attributes>` +
			`<cas:memberOf><cas:x>ops</cas:x></cas:memberOf></cas:attributes>` + closing},
		{name: "two attributes elements", body: open + `<cas:user>a</cas:user><cas:attributes/><cas:attributes/>` +
			closing},
		{name: "a proxy-granting ticket", body: open + `<cas:user>a</cas:user>` +
			`<cas:proxyGrantingTicket>PGTIOU-1</cas:proxyGrantingTicket>` + closing},
		{name: "a failure code with lowercase", body: `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">` +
			`<cas:authenticationFailure code="invalid"/></cas:serviceResponse>`},
		{name: "an answer over the size limit", body: open + "<cas:user>" +
			strings.Repeat("a", casauth.MaxAnswerBytes) + "</cas:user>" + closing},
	}

	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, err := casauth.ParseResponse([]byte(tc.body)); !errors.Is(err, casauth.ErrAnswer) {
				t.Fatalf("ParseResponse() = %+v, %v, want ErrAnswer", got, err)
			}
		})
	}
}

// roots returns the count of the elements at the top level of b, read
// with its own decoder. It returns -1 for an end tag with no open element
// and for text outside the elements that is not whitespace, so that the
// caller sees each of them as a wrong count. It returns 1 when the decoder
// cannot read b, so the caller checks only what the decoder can read.
func roots(b []byte) int {
	d := xml.NewDecoder(bytes.NewReader(b))

	var c rootCount

	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			return c.n
		}

		if err != nil {
			return 1
		}

		if !c.add(tok) {
			return -1
		}
	}
}

// rootCount is the state of roots: the depth and the count of the
// elements at the top level.
type rootCount struct {
	depth int
	n     int
}

// add counts tok. It returns false for an end tag with no open element
// and for text outside the elements that is not whitespace.
func (c *rootCount) add(tok xml.Token) bool {
	switch v := tok.(type) {
	case xml.StartElement:
		if c.depth == 0 {
			c.n++
		}

		c.depth++
	case xml.EndElement:
		c.depth--

		return c.depth >= 0
	case xml.CharData:
		return c.depth != 0 || len(bytes.TrimSpace(v)) == 0
	default:
	}

	return true
}

// FuzzParseResponse checks that the parser never panics, and that each
// answer that it accepts has one element at the top level and is exactly
// one success with a user or one failure with a code.
func FuzzParseResponse(f *testing.F) {
	f.Add([]byte(open + "<cas:user>alice</cas:user><cas:attributes><cas:a>b</cas:a></cas:attributes>" + closing))
	f.Add([]byte(`<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">` +
		`<cas:authenticationFailure code="INVALID_TICKET"/></cas:serviceResponse>`))
	f.Add([]byte(`<!DOCTYPE x [<!ENTITY a "b">]>` + open + "<cas:user>&a;</cas:user>" + closing))
	f.Add([]byte(`<?xml version="1.0"?>`))

	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := casauth.ParseResponse(b)
		if err != nil {
			if !errors.Is(err, casauth.ErrAnswer) {
				t.Fatalf("ParseResponse() = %v, want ErrAnswer", err)
			}

			return
		}

		if len(b) > casauth.MaxAnswerBytes || strings.Contains(string(b), "<!ENTITY") ||
			strings.Contains(string(b), "<!DOCTYPE") {
			t.Fatal("ParseResponse() accepted an answer with a declaration or over the limit")
		}

		if n := roots(b); n != 1 {
			t.Fatalf("ParseResponse() accepted an answer with %d elements at the top level, want 1", n)
		}

		if got.Success == (got.FailureCode != "") {
			t.Fatalf("ParseResponse() = %+v, want a success or a failure", got)
		}
	})
}
