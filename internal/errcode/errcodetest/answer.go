package errcodetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// Shape is the wire shape of an error answer.
type Shape int

// The shapes of an error answer.
const (
	// ShapeCamelCase is the shape of the Pulumi Cloud-compatible API and of
	// the tool-neutral APIs: the status in "code", the machine code in
	// "errorCode" and the arguments in "errorArgs".
	ShapeCamelCase Shape = iota + 1
	// ShapeJSONAPI is the shape of the TFE-compatible API: the first entry of
	// "errors" has the status in "status", the machine code in "code" and the
	// arguments in "meta.args".
	ShapeJSONAPI
)

// Want is the answer that a test expects: the code and the arguments. A
// quantity is an int64, an identifier and a time are strings. Args is nil for
// a code without arguments. RequestBody is the JSON body of the request of the
// test, or nil: CheckAnswer fails when an argument holds a value of it.
type Want struct {
	Code        string
	Args        map[string]any
	RequestBody []byte
}

// wire is the code and the arguments that a body holds, in any shape.
type wire struct {
	status string
	code   string
	args   map[string]any
}

// errEntries is the error of a JSON:API document that has not one entry.
var errEntries = errors.New("the document needs exactly one entry in errors")

// CheckAnswer fails the test unless body, the answer of status in shape, holds
// the code and the arguments of want. It also fails when:
//   - the code is not in the catalog;
//   - an argument is not declared by the code, or a declared argument is
//     missing;
//   - the value of an argument equals a value in want.RequestBody. An argument
//     never holds a value that the caller sent.
func CheckAnswer(tb testing.TB, shape Shape, status int, body []byte, want Want) {
	tb.Helper()

	got, err := parseWire(shape, body)
	if err != nil {
		tb.Fatalf("answer is not %s: %v: %s", shapeName(shape), err, body)
	}

	if got.status != strconv.Itoa(status) {
		tb.Fatalf("status = %s, want %d: %s", got.status, status, body)
	}

	if got.code != want.Code {
		tb.Fatalf("%s = %q, want %q: %s", codeMember(shape), got.code, want.Code, body)
	}

	spec, ok := errcode.Lookup(got.code)
	if !ok {
		tb.Fatalf("the code %q is not in the catalog: %s", got.code, body)
	}

	checkArgs(tb, spec, got.args, want, body)
}

// checkArgs fails the test unless args are the arguments of want, and exactly
// the arguments that spec declares, and none is a value of the request.
func checkArgs(tb testing.TB, spec errcode.Spec, args map[string]any, want Want, body []byte) {
	tb.Helper()

	if len(args) == 0 {
		args = nil
	}

	for name := range args {
		if !slices.ContainsFunc(spec.Args, func(a errcode.Arg) bool { return a.Name == name }) {
			tb.Fatalf("the code %q does not declare the argument %q: %s", spec.Code, name, body)
		}
	}

	wanted, err := normalize(want.Args)
	if err != nil {
		tb.Fatalf("want arguments %v: %v", want.Args, err)
	}

	if len(args) != len(spec.Args) || !reflect.DeepEqual(args, wanted) {
		tb.Fatalf("arguments = %v, want %v, the code declares %d: %s", args, wanted, len(spec.Args), body)
	}

	sent := leaves(want.RequestBody)

	for name, value := range args {
		if slices.Contains(sent, leaf(value)) {
			tb.Fatalf("the argument %q holds %v, a value of the request body: %s", name, value, body)
		}
	}
}

// shapeName names shape for a failure message.
func shapeName(shape Shape) string {
	if shape == ShapeJSONAPI {
		return "a JSON:API error document"
	}

	return "a JSON error body"
}

// codeMember names the member of shape that holds the machine code.
func codeMember(shape Shape) string {
	if shape == ShapeJSONAPI {
		return "code"
	}

	return "errorCode"
}

// jsonAPIDoc is the part of a JSON:API error document that a check reads.
type jsonAPIDoc struct {
	Errors []jsonAPIError `json:"errors"`
}

// jsonAPIError is one entry of a JSON:API error document.
type jsonAPIError struct {
	Status string      `json:"status"`
	Code   string      `json:"code"`
	Meta   jsonAPIMeta `json:"meta"`
}

// jsonAPIMeta is the meta member of an entry.
type jsonAPIMeta struct {
	Args map[string]any `json:"args"`
}

// parseWire reads the status, the code and the arguments from body.
func parseWire(shape Shape, body []byte) (wire, error) {
	if shape == ShapeJSONAPI {
		var doc jsonAPIDoc

		if err := json.Unmarshal(body, &doc); err != nil {
			return wire{}, fmt.Errorf("decode: %w", err)
		}

		if len(doc.Errors) != 1 {
			return wire{}, fmt.Errorf("%w: %d entries", errEntries, len(doc.Errors))
		}

		e := doc.Errors[0]

		return wire{status: e.Status, code: e.Code, args: e.Meta.Args}, nil
	}

	var doc struct {
		Code      int            `json:"code"`
		ErrorCode string         `json:"errorCode"`
		ErrorArgs map[string]any `json:"errorArgs"`
	}

	if err := json.Unmarshal(body, &doc); err != nil {
		return wire{}, fmt.Errorf("decode: %w", err)
	}

	return wire{status: strconv.Itoa(doc.Code), code: doc.ErrorCode, args: doc.ErrorArgs}, nil
}

// normalize returns args as JSON decoding gives them, so that a quantity is a
// float64. It returns nil for no argument.
func normalize(args map[string]any) (map[string]any, error) {
	var out map[string]any

	if len(args) == 0 {
		return out, nil
	}

	data, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return out, nil
}

// leaves returns every string and number of the JSON document body as text,
// at any depth. A body that is not JSON gives its whole text.
func leaves(body []byte) []string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil
	}

	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return []string{text}
	}

	var out []string

	var walk func(v any)

	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			if x != "" {
				out = append(out, x)
			}
		case float64:
			out = append(out, leaf(x))
		default:
		}
	}

	walk(doc)

	return out
}

// leaf returns the text of a JSON scalar.
func leaf(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}

	return fmt.Sprint(v)
}
