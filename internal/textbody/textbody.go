// Package textbody checks the text of a JSON request body before a decode
// changes it. encoding/json replaces each byte that is not valid UTF-8 and
// each lone surrogate escape with U+FFFD, so a check made after the decode
// cannot see what the client sent.
package textbody

import (
	"encoding/json/jsontext"
	"unicode/utf8"
)

// Refused reports whether body holds text that a JSON decode would change: a
// byte that is not valid UTF-8, or a lone surrogate escape in a string. A body
// with a syntax fault and no such text is not refused, so its decode keeps its
// own answer.
func Refused(body []byte) bool {
	return !utf8.Valid(body) || loneSurrogateEscape(body)
}

// loneSurrogateEscape reports whether body is JSON with a lone surrogate
// escape and with no other fault of syntax or of UTF-8. Such a body is valid
// once invalid UTF-8 is allowed, and it is not valid with the defaults.
func loneSurrogateEscape(body []byte) bool {
	v := jsontext.Value(body)

	return !v.IsValid(jsontext.AllowDuplicateNames(true)) &&
		v.IsValid(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
}
