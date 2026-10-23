package textbody

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// The errors of Read, Unmarshal and Decode. No error holds a part of the
// body, so an adapter can put the text of an error in its answer.
var (
	// ErrTooLarge reports a body over the limit. The error is a
	// *TooLargeError, which holds the limit.
	ErrTooLarge = errors.New("request body is too large")
	// ErrUnreadable reports a body that the server could not read to its end.
	ErrUnreadable = errors.New("request body is not readable")
	// ErrEmpty reports an empty body on a route that needs one.
	ErrEmpty = errors.New("request body is empty")
	// ErrText reports a body with text that a JSON decode would change: a
	// byte that is not valid UTF-8, or a lone surrogate escape.
	ErrText = errors.New("request body is not valid UTF-8 text")
	// ErrJSON reports a body that is not one JSON value of the expected
	// shape: a syntax fault, a field of the wrong type, an unknown field, or
	// data after the value.
	ErrJSON = errors.New("request body is not one JSON value with known fields")
)

// TooLargeError is the error of a body over the limit. It matches
// ErrTooLarge.
type TooLargeError struct {
	// Limit is the largest body, in bytes, that the route reads.
	Limit int64
}

// Error returns the text of the refusal with the limit.
func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%s: the limit is %d bytes", ErrTooLarge, e.Limit)
}

// Is reports whether target is ErrTooLarge.
func (e *TooLargeError) Is(target error) bool {
	return target == ErrTooLarge
}

// Option changes a rule of Unmarshal and Decode.
type Option func(*rules)

// rules are the rules of one decode. The zero value is the strict default.
type rules struct {
	allowEmpty   bool
	allowUnknown bool
}

// AllowEmpty makes an empty body valid. Unmarshal then returns nil and does
// not change v. A route whose body is optional uses it.
func AllowEmpty() Option {
	return func(r *rules) { r.allowEmpty = true }
}

// AllowUnknownFields makes a field that v does not declare valid. The decode
// ignores it. A route uses it only when its contract says that a client may
// send fields that the server does not know.
func AllowUnknownFields() Option {
	return func(r *rules) { r.allowUnknown = true }
}

// Read reads the body of r, at most limit bytes. It returns a *TooLargeError
// for a body over the limit, and ErrUnreadable for any other read fault. A
// limit of zero or less refuses every body that is not empty.
func Read(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	// Fail closed: a limit of zero or less is a fault of the caller, so the
	// read refuses each byte instead of reading without a limit.
	limit = max(limit, 0)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
		return nil, &TooLargeError{Limit: limit}
	}

	if err != nil {
		return nil, ErrUnreadable
	}

	return body, nil
}

// Unmarshal decodes body into v. It refuses, in this order: an empty body
// with ErrEmpty (unless AllowEmpty), text that a decode would change with
// ErrText, and a body that is not one JSON value with only the fields of v
// with ErrJSON (unless AllowUnknownFields). It checks the text before the
// decode, because encoding/json replaces each bad byte and each lone
// surrogate escape with U+FFFD, and a check after the decode cannot see them.
func Unmarshal(body []byte, v any, opts ...Option) error {
	var rule rules

	for _, opt := range opts {
		opt(&rule)
	}

	if len(bytes.TrimSpace(body)) == 0 {
		if rule.allowEmpty {
			return nil
		}

		return ErrEmpty
	}

	if Refused(body) {
		return ErrText
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	if !rule.allowUnknown {
		dec.DisallowUnknownFields()
	}

	if err := dec.Decode(v); err != nil {
		return ErrJSON
	}

	// A second value, or any data after the first value, refuses the body.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrJSON
	}

	return nil
}

// Decode reads the body of r, at most limit bytes, and decodes it into v as
// Unmarshal does. It returns the errors of Read and of Unmarshal.
func Decode(w http.ResponseWriter, r *http.Request, limit int64, v any, opts ...Option) error {
	body, err := Read(w, r, limit)
	if err != nil {
		return err
	}

	return Unmarshal(body, v, opts...)
}
