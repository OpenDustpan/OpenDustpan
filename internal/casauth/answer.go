package casauth

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// This file holds the parser of the answer of a CAS 3.0 service ticket
// validation. It uses encoding/xml only. It refuses a document type
// declaration, each entity other than the five of XML, a processing
// instruction other than the XML declaration, and a depth over
// MaxAnswerDepth, before it reads a value.

// Limits of an answer.
const (
	// MaxAnswerBytes is the largest answer of the CAS server, in bytes.
	MaxAnswerBytes = 64 << 10
	// MaxAnswerDepth is the deepest nesting of elements in an answer.
	MaxAnswerDepth = 64
	// maxFailureCode is the longest failure code, in bytes.
	maxFailureCode = 64
	// maxAttributeValues is the most attribute values of one answer.
	maxAttributeValues = 4096
)

// ErrAnswer reports an answer that is not a valid CAS 3.0 service response.
var ErrAnswer = errors.New("the CAS answer is not valid")

// Response is a parsed CAS 3.0 service response. It is either a success
// or a failure.
type Response struct {
	// Attributes holds the values of each attribute of a success, by the
	// local name of the attribute, in the order of the answer.
	Attributes map[string][]string
	// User is the CAS user of a success.
	User string
	// FailureCode is the code of a failure.
	FailureCode string
	// Success is true for a success.
	Success bool
}

// xmlResponse is the root element of an answer.
type xmlResponse struct {
	XMLName xml.Name     `xml:"http://www.yale.edu/tp/cas serviceResponse"`
	Success []xmlSuccess `xml:"http://www.yale.edu/tp/cas authenticationSuccess"`
	Failure []xmlFailure `xml:"http://www.yale.edu/tp/cas authenticationFailure"`
	Other   []xmlAny     `xml:",any"`
}

// xmlSuccess is the element authenticationSuccess.
type xmlSuccess struct {
	User       []xmlValue      `xml:"http://www.yale.edu/tp/cas user"`
	Attributes []xmlAttributes `xml:"http://www.yale.edu/tp/cas attributes"`
	PGT        []xmlAny        `xml:"http://www.yale.edu/tp/cas proxyGrantingTicket"`
	Proxies    []xmlAny        `xml:"http://www.yale.edu/tp/cas proxies"`
}

// xmlAttributes is the element attributes.
type xmlAttributes struct {
	Values []xmlValue `xml:",any"`
}

// xmlValue is one attribute value.
type xmlValue struct {
	XMLName xml.Name `xml:""`
	Text    string   `xml:",chardata"`
	Inner   []xmlAny `xml:",any"`
}

// xmlFailure is the element authenticationFailure.
type xmlFailure struct {
	Code string `xml:"code,attr"`
}

// xmlAny is an element whose name and content the parser does not read.
type xmlAny struct{}

// ParseResponse parses the answer b of a service ticket validation. It
// returns ErrAnswer for an answer over MaxAnswerBytes, an answer that is not
// UTF-8, a document type declaration, an entity other than the five of
// XML, a processing instruction other than the XML declaration, a depth
// over MaxAnswerDepth, a root element that is not a service response, and a
// response that is not exactly one success or one failure. A success must
// hold exactly one user, at most one attributes element, no proxy-granting
// ticket and no proxies, and attribute values of the CAS namespace with
// text only.
func ParseResponse(b []byte) (Response, error) {
	var none Response

	if err := checkDocument(b); err != nil {
		return none, err
	}

	var doc xmlResponse

	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true

	if err := d.Decode(&doc); err != nil {
		return none, fmt.Errorf("%w: decode: %w", ErrAnswer, err)
	}

	switch {
	case len(doc.Other) > 0:
		return none, fmt.Errorf("%w: the response holds an unknown element", ErrAnswer)
	case len(doc.Success)+len(doc.Failure) != 1:
		return none, fmt.Errorf("%w: the response must hold exactly one success or one failure", ErrAnswer)
	case len(doc.Failure) == 1:
		return failure(doc.Failure[0])
	default:
		// The tag of xmlResponse holds the namespace of the CAS protocol, so
		// the root has it.
		return success(doc.Success[0], doc.XMLName.Space)
	}
}

// checkDocument checks the size, the encoding, the depth and each token of
// b. It refuses each token that could expand or fetch content.
func checkDocument(b []byte) error {
	switch {
	case len(b) > MaxAnswerBytes:
		return fmt.Errorf("%w: the answer is larger than %d bytes", ErrAnswer, MaxAnswerBytes)
	case !utf8.Valid(b):
		return fmt.Errorf("%w: the answer is not UTF-8", ErrAnswer)
	}

	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	// No CharsetReader: a declared encoding other than UTF-8 fails.

	var sc scanner

	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return fmt.Errorf("%w: read a token: %w", ErrAnswer, err)
		}

		if checkErr := sc.check(tok); checkErr != nil {
			return checkErr
		}
	}

	// Defense in depth: the decoder in strict mode refuses an entity that
	// XML does not define, and the loop above refuses each declaration.
	// This check stays so that a change of the decoder cannot pass one.
	if bytes.Contains(b, []byte("<!ENTITY")) || bytes.Contains(b, []byte("<!DOCTYPE")) {
		return fmt.Errorf("%w: the answer holds a declaration", ErrAnswer)
	}

	return nil
}

// scanner is the state of the check of the tokens of one answer.
type scanner struct {
	// depth is the nesting of the elements before the next token.
	depth int
	// n is the index of the next token.
	n int
	// closed is true after the end of the root element.
	closed bool
}

// check checks the next token of an answer and updates the state.
func (sc *scanner) check(tok xml.Token) error {
	defer func() { sc.n++ }()

	switch v := tok.(type) {
	case xml.Directive:
		return fmt.Errorf("%w: the answer holds a document type declaration or a directive", ErrAnswer)
	case xml.ProcInst:
		if sc.n != 0 || v.Target != "xml" {
			return fmt.Errorf("%w: the answer holds a processing instruction", ErrAnswer)
		}
	case xml.StartElement:
		if sc.closed {
			// The decoder reads the first root only. A second root, or any
			// element after the first one, is refused so that no value
			// outside the read root can be in the answer.
			return fmt.Errorf("%w: the answer holds an element after its root", ErrAnswer)
		}

		if sc.depth+1 > MaxAnswerDepth {
			return fmt.Errorf("%w: the answer is deeper than %d elements", ErrAnswer, MaxAnswerDepth)
		}

		sc.depth++
	case xml.EndElement:
		sc.depth--
		sc.closed = sc.depth == 0
	case xml.CharData, xml.Comment:
	default:
		// Fail closed: the decoder has no other token type today.
		return fmt.Errorf("%w: the answer holds an unknown token", ErrAnswer)
	}

	return nil
}

// failure returns the Response of a failure element.
func failure(f xmlFailure) (Response, error) {
	code := strings.TrimSpace(f.Code)
	if code == "" || len(code) > maxFailureCode || strings.ContainsFunc(code, notCodeRune) {
		return Response{}, fmt.Errorf("%w: the failure code is not valid", ErrAnswer)
	}

	return Response{Attributes: nil, User: "", FailureCode: code, Success: false}, nil
}

// notCodeRune reports whether r is outside the characters of a failure
// code: A-Z, 0-9 and the underscore.
func notCodeRune(r rune) bool {
	return (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_'
}

// success returns the Response of a success element. Each attribute must
// be in the namespace ns of the CAS protocol.
func success(s xmlSuccess, ns string) (Response, error) {
	var none Response

	switch {
	case len(s.PGT) > 0 || len(s.Proxies) > 0:
		// No pgtUrl is ever sent, so a success with a proxy-granting ticket
		// or with proxies is the answer of a proxy ticket.
		return none, fmt.Errorf("%w: the answer is of a proxy ticket", ErrAnswer)
	case len(s.User) != 1:
		return none, fmt.Errorf("%w: the success must hold exactly one user", ErrAnswer)
	case len(s.User[0].Inner) > 0:
		// The decoder skips an element inside a text field, so the user
		// keeps a record of each element and refuses it, as each attribute
		// value does.
		return none, fmt.Errorf("%w: the user holds an element", ErrAnswer)
	case len(s.Attributes) > 1:
		return none, fmt.Errorf("%w: the success holds more than one attributes element", ErrAnswer)
	}

	attrs := map[string][]string{}
	count := 0

	for _, list := range s.Attributes {
		for _, v := range list.Values {
			switch {
			case v.XMLName.Space != ns:
				return none, fmt.Errorf("%w: an attribute is not in the CAS namespace", ErrAnswer)
			case len(v.Inner) > 0:
				return none, fmt.Errorf("%w: an attribute value holds an element", ErrAnswer)
			}

			count++
			if count > maxAttributeValues {
				return none, fmt.Errorf("%w: more than %d attribute values", ErrAnswer, maxAttributeValues)
			}

			attrs[v.XMLName.Local] = append(attrs[v.XMLName.Local], strings.TrimSpace(v.Text))
		}
	}

	return Response{Attributes: attrs, User: strings.TrimSpace(s.User[0].Text), FailureCode: "", Success: true}, nil
}
